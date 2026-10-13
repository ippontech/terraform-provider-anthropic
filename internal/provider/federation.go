// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// identityTokenCommandTimeout bounds one run of identity_token_command. The
// command runs on every token exchange, so a hung command would otherwise
// stall the request that triggered the exchange indefinitely. Passed to
// commandIdentityToken as an argument, not read as a package variable, so a
// test can shorten it without racing the other tests of the package.
const identityTokenCommandTimeout = 30 * time.Second

// identityTokenCommandWaitDelay bounds the wait for the command's output
// pipes after it is killed. The kill reaches only the program itself: the
// children of `sh -c "curl ... | jq"` keep stdout open, and without a
// WaitDelay, Run waits for them however long they hang.
const identityTokenCommandWaitDelay = 5 * time.Second

// federationModel is the provider's federation block.
type federationModel struct {
	OrganizationID       types.String `tfsdk:"organization_id"`
	FederationRuleID     types.String `tfsdk:"federation_rule_id"`
	ServiceAccountID     types.String `tfsdk:"service_account_id"`
	WorkspaceID          types.String `tfsdk:"workspace_id"`
	IdentityToken        types.String `tfsdk:"identity_token"`
	IdentityTokenFile    types.String `tfsdk:"identity_token_file"`
	IdentityTokenCommand types.List   `tfsdk:"identity_token_command"`
}

func federationSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "Obtain the org:admin OAuth bearer token through Workload Identity Federation instead of `auth_token`. " +
			"The provider exchanges an identity token from your platform (a CI OIDC token, a Kubernetes projected " +
			"service account token, an Entra token...) for an Anthropic access token, and exchanges again before it " +
			"expires, fetching a fresh identity token each time. Every attribute falls back to the environment " +
			"variable the Anthropic SDKs read. Conflicts with `auth_token`; an exported ANTHROPIC_AUTH_TOKEN is " +
			"ignored, with a warning. Give the federation rule a " +
			"`token_lifetime_seconds` well above 120: the SDK refreshes within the last 120 seconds of a token's life, " +
			"so a shorter lifetime refreshes, and fetches an identity token, on nearly every request.",
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				Optional:    true,
				Description: "UUID of the Anthropic organization. Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.",
			},
			"federation_rule_id": schema.StringAttribute{
				Optional:    true,
				Description: "Federation rule (`fdrl_...`) to exchange against. Can also be set via the ANTHROPIC_FEDERATION_RULE_ID environment variable.",
			},
			"service_account_id": schema.StringAttribute{
				Optional:    true,
				Description: "Service account (`svac_...`) the rule targets; the exchange fails if the rule targets another. Can also be set via the ANTHROPIC_SERVICE_ACCOUNT_ID environment variable.",
			},
			"workspace_id": schema.StringAttribute{
				Optional: true,
				Description: "Workspace (`wrkspc_...`) to scope the token to at exchange time. Required only when the rule is enabled for " +
					"more than one workspace. Can also be set via the ANTHROPIC_WORKSPACE_ID environment variable, which the " +
					"SDKs' own federation reads the same way.",
			},
			"identity_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "A literal identity token (JWT). It cannot be refreshed, so applies that outlive it fail; prefer " +
					"`identity_token_file` or `identity_token_command`. Can also be set via the ANTHROPIC_IDENTITY_TOKEN environment variable.",
			},
			"identity_token_file": schema.StringAttribute{
				Optional: true,
				Description: "Path to a file holding the identity token, re-read on every exchange so a rotating file " +
					"(such as a Kubernetes projected token) stays current. The file must have been rotated by the next exchange " +
					"if its token carries a `jti`, which the exchange accepts only once. " +
					"Can also be set via the ANTHROPIC_IDENTITY_TOKEN_FILE environment variable.",
			},
			"identity_token_command": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Command (program and arguments) that prints an identity token on stdout. The program is run " +
					"directly, not through a shell: for a pipe or `$VARIABLE` expansion, make the shell the program " +
					"(`[\"sh\", \"-c\", \"...\"]`). It runs on every exchange, so short-lived tokens, and tokens with a `jti` " +
					"claim that the exchange accepts only once, are fetched fresh each time. It is stopped after 30 seconds. " +
					"Its stderr is not shown in diagnostics, since a failing CLI may echo the token there; it is logged " +
					"at TF_LOG=DEBUG.",
			},
		},
	}
}

// federationSettings is the federation block after environment fallback.
type federationSettings struct {
	options  option.FederationOptions
	identity option.IdentityTokenFunc
}

// resolveFederation returns nil when the federation block is absent. Each
// attribute falls back to its environment variable, as the SDKs' own
// federation does. The identity token source may come from config or the
// environment, but config sources win, and two configured sources are an
// error rather than a silent precedence. An attribute unknown at plan time is
// an error: treating it as unset would configure a different source, or
// report a missing value that is in fact written in the configuration.
func resolveFederation(ctx context.Context, obj types.Object) (*federationSettings, diag.Diagnostics) {
	var diags diag.Diagnostics
	if obj.IsNull() {
		return nil, diags
	}
	if obj.IsUnknown() {
		diags.AddAttributeError(path.Root("federation"), "Unknown Federation Configuration",
			"The federation block depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return nil, diags
	}

	var m federationModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}

	// A slice, not a map, so several unknown attributes are reported in the
	// same order on every plan.
	for _, a := range []struct {
		name  string
		value attr.Value
	}{
		{"organization_id", m.OrganizationID},
		{"federation_rule_id", m.FederationRuleID},
		{"service_account_id", m.ServiceAccountID},
		{"workspace_id", m.WorkspaceID},
		{"identity_token", m.IdentityToken},
		{"identity_token_file", m.IdentityTokenFile},
		{"identity_token_command", m.IdentityTokenCommand},
	} {
		if containsUnknown(a.value) {
			diags.AddAttributeError(path.Root("federation").AtName(a.name), "Unknown Federation Configuration",
				fmt.Sprintf("federation.%s depends on a value that is not known until apply. Provider configuration must be known at plan time.", a.name))
		}
	}
	if diags.HasError() {
		return nil, diags
	}

	s := &federationSettings{options: option.FederationOptions{
		OrganizationID:   resolveCredential(m.OrganizationID, "ANTHROPIC_ORGANIZATION_ID"),
		FederationRuleID: resolveCredential(m.FederationRuleID, "ANTHROPIC_FEDERATION_RULE_ID"),
		ServiceAccountID: resolveCredential(m.ServiceAccountID, "ANTHROPIC_SERVICE_ACCOUNT_ID"),
		WorkspaceID:      resolveCredential(m.WorkspaceID, "ANTHROPIC_WORKSPACE_ID"),
	}}

	if s.options.OrganizationID == "" {
		diags.AddAttributeError(path.Root("federation").AtName("organization_id"), "Missing Federation Organization",
			"Set federation.organization_id or the ANTHROPIC_ORGANIZATION_ID environment variable.")
	}
	if s.options.FederationRuleID == "" {
		diags.AddAttributeError(path.Root("federation").AtName("federation_rule_id"), "Missing Federation Rule",
			"Set federation.federation_rule_id or the ANTHROPIC_FEDERATION_RULE_ID environment variable.")
	}

	identity, err := identityTokenSource(ctx, m)
	if err != nil {
		diags.AddAttributeError(path.Root("federation"), "Invalid Identity Token Source", err.Error())
	}
	s.identity = identity

	if diags.HasError() {
		return nil, diags
	}
	return s, diags
}

// identityTokenSource expects no unknown attribute; resolveFederation rejects
// those first.
//
// The environment fills the source as a whole, not attribute by attribute
// the way resolveCredential does: CI jobs export ANTHROPIC_IDENTITY_TOKEN_FILE
// for other tools, and a configured command beside it is not a conflict.
func identityTokenSource(ctx context.Context, m federationModel) (option.IdentityTokenFunc, error) {
	var argv []string
	if !m.IdentityTokenCommand.IsNull() {
		if d := m.IdentityTokenCommand.ElementsAs(ctx, &argv, false); d.HasError() {
			return nil, errors.New("identity_token_command must be a list of strings")
		}
		if len(argv) == 0 || argv[0] == "" {
			return nil, errors.New("identity_token_command must name a program")
		}
	}
	file, literal := m.IdentityTokenFile.ValueString(), m.IdentityToken.ValueString()

	configured := 0
	for _, set := range []bool{argv != nil, file != "", literal != ""} {
		if set {
			configured++
		}
	}
	if configured > 1 {
		return nil, errors.New("set at most one of identity_token, identity_token_file and identity_token_command")
	}
	if configured == 0 {
		// Same order as the SDKs below: the file, which can rotate, before the literal.
		file, literal = os.Getenv("ANTHROPIC_IDENTITY_TOKEN_FILE"), os.Getenv("ANTHROPIC_IDENTITY_TOKEN")
	}

	switch {
	case argv != nil:
		return commandIdentityToken(argv, identityTokenCommandTimeout), nil
	case file != "":
		return option.IdentityTokenFile(file), nil
	case literal != "":
		return staticIdentityToken(literal), nil
	}
	return nil, errors.New("no identity token: set identity_token_file, identity_token_command or identity_token, " +
		"or the ANTHROPIC_IDENTITY_TOKEN_FILE or ANTHROPIC_IDENTITY_TOKEN environment variable")
}

// containsUnknown reports whether v, or any element of a list v, is unknown.
func containsUnknown(v attr.Value) bool {
	if v.IsUnknown() {
		return true
	}
	if l, ok := v.(types.List); ok {
		return slices.ContainsFunc(l.Elements(), func(e attr.Value) bool { return e.IsUnknown() })
	}
	return false
}

func staticIdentityToken(token string) option.IdentityTokenFunc {
	token = strings.TrimSpace(token)
	return func(context.Context) (string, error) { return token, nil }
}

// commandIdentityToken runs argv for each exchange, stopping it after
// timeout. Neither of its outputs reaches the error, which Terraform prints
// and CI logs keep: on a failure, stdout may hold a token, and stderr may
// echo one (`curl -v`, `az --debug`, a script under `set -x`). stderr goes to
// the debug log instead, which the operator turns on deliberately.
func commandIdentityToken(argv []string, timeout time.Duration) option.IdentityTokenFunc {
	return func(ctx context.Context) (string, error) {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmd.WaitDelay = identityTokenCommandWaitDelay
		err := cmd.Run()
		// The program exited normally but a child it left behind (a daemon
		// started with `&`) still holds stdout open: the token it printed is
		// whole, so keep it.
		if errors.Is(err, exec.ErrWaitDelay) && strings.TrimSpace(stdout.String()) != "" {
			err = nil
		}
		if err != nil {
			tflog.Debug(ctx, "identity_token_command failed", map[string]any{
				"program": argv[0],
				"stderr":  strings.TrimSpace(stderr.String()),
			})
			switch {
			case errors.Is(runCtx.Err(), context.DeadlineExceeded):
				return "", fmt.Errorf("identity_token_command %q did not finish within %s (its stderr is logged at TF_LOG=DEBUG)",
					argv[0], timeout)
			case ctx.Err() != nil:
				// Cancelled by the caller, not failed: the command's stderr
				// holds nothing to look up.
				return "", ctx.Err()
			}
			return "", fmt.Errorf("identity_token_command %q: %w (its stderr is logged at TF_LOG=DEBUG)", argv[0], err)
		}
		token := strings.TrimSpace(stdout.String())
		if token == "" {
			return "", fmt.Errorf("identity_token_command %q printed no token", argv[0])
		}
		return token, nil
	}
}
