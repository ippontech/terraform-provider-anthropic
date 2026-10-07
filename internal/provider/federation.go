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
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// identityTokenCommandTimeout bounds one run of identity_token_command. The
// command runs on every token exchange, so a hung command would otherwise
// stall the request that triggered the exchange indefinitely.
const identityTokenCommandTimeout = 30 * time.Second

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
			"expires, fetching a fresh identity token each time. Every attribute but `workspace_id` falls back to the " +
			"environment variable the Anthropic SDKs read. Conflicts with `auth_token`.",
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
					"more than one workspace. Configuration only: ANTHROPIC_WORKSPACE_ID is not read here, because it names the " +
					"workspace a request is sent to, and the org:admin endpoints ignore this value anyway.",
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
					"(such as a Kubernetes projected token) stays current. Can also be set via the ANTHROPIC_IDENTITY_TOKEN_FILE environment variable.",
			},
			"identity_token_command": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Command (program and arguments) that prints an identity token on stdout. The program is run " +
					"directly, not through a shell: for a pipe or `$VARIABLE` expansion, make the shell the program " +
					"(`[\"sh\", \"-c\", \"...\"]`). It runs on every exchange, so tokens that are single-use or short-lived " +
					"are fetched fresh each time.",
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
// attribute but workspace_id falls back to its environment variable;
// ANTHROPIC_WORKSPACE_ID selects the workspace a request is sent to, which a
// federated token's exchange-time scope is not, so the two stay separate. The
// identity token source
// may come from config or the environment, but config sources win, and two
// configured sources are an error rather than a silent precedence.
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

	s := &federationSettings{options: option.FederationOptions{
		OrganizationID:   resolveCredential(m.OrganizationID, "ANTHROPIC_ORGANIZATION_ID"),
		FederationRuleID: resolveCredential(m.FederationRuleID, "ANTHROPIC_FEDERATION_RULE_ID"),
		ServiceAccountID: resolveCredential(m.ServiceAccountID, "ANTHROPIC_SERVICE_ACCOUNT_ID"),
		WorkspaceID:      m.WorkspaceID.ValueString(),
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

func identityTokenSource(ctx context.Context, m federationModel) (option.IdentityTokenFunc, error) {
	var argv []string
	if !m.IdentityTokenCommand.IsNull() && !m.IdentityTokenCommand.IsUnknown() {
		var elems []types.String
		if d := m.IdentityTokenCommand.ElementsAs(ctx, &elems, false); d.HasError() {
			return nil, errors.New("identity_token_command must be a list of known strings")
		}
		for _, e := range elems {
			argv = append(argv, e.ValueString())
		}
		if len(argv) == 0 || argv[0] == "" {
			return nil, errors.New("identity_token_command must name a program")
		}
	}

	configured := 0
	for _, set := range []bool{knownNonEmpty(m.IdentityToken), knownNonEmpty(m.IdentityTokenFile), argv != nil} {
		if set {
			configured++
		}
	}
	if configured > 1 {
		return nil, errors.New("set at most one of identity_token, identity_token_file and identity_token_command")
	}

	switch {
	case argv != nil:
		return commandIdentityToken(argv), nil
	case knownNonEmpty(m.IdentityTokenFile):
		return option.IdentityTokenFile(m.IdentityTokenFile.ValueString()), nil
	case knownNonEmpty(m.IdentityToken):
		return staticIdentityToken(m.IdentityToken.ValueString()), nil
	}

	// Same order as the SDKs: the file, which can rotate, before the literal.
	if p := os.Getenv("ANTHROPIC_IDENTITY_TOKEN_FILE"); p != "" {
		return option.IdentityTokenFile(p), nil
	}
	if v := os.Getenv("ANTHROPIC_IDENTITY_TOKEN"); v != "" {
		return staticIdentityToken(v), nil
	}
	return nil, errors.New("no identity token: set identity_token_file, identity_token_command or identity_token, " +
		"or the ANTHROPIC_IDENTITY_TOKEN_FILE or ANTHROPIC_IDENTITY_TOKEN environment variable")
}

func knownNonEmpty(v attr.Value) bool {
	s, ok := v.(types.String)
	return ok && !s.IsNull() && !s.IsUnknown() && s.ValueString() != ""
}

func staticIdentityToken(token string) option.IdentityTokenFunc {
	token = strings.TrimSpace(token)
	return func(context.Context) (string, error) { return token, nil }
}

// commandIdentityToken runs argv for each exchange. stderr is included in the
// error so a failing `az` or `gcloud` explains itself, but stdout never is: on
// a partial failure it may hold a token.
func commandIdentityToken(argv []string) option.IdentityTokenFunc {
	return func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, identityTokenCommandTimeout)
		defer cancel()

		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("identity_token_command %q: %w: %s", argv[0], err, strings.TrimSpace(stderr.String()))
		}
		token := strings.TrimSpace(stdout.String())
		if token == "" {
			return "", fmt.Errorf("identity_token_command %q printed no token", argv[0])
		}
		return token, nil
	}
}
