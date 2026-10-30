// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// apiFederationValue builds the api_federation block from attrs, every other
// attribute null.
func apiFederationValue(t *testing.T, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	return nestedValue(t, "api_federation", attrs)
}

// TestConfigureAPIFederationBacksTheStandardClient covers the point of the
// block: the standard client, which every workspace resource uses, carries a
// bearer minted from a workspace-scoped rule instead of an API key, and
// re-exchanges a fresh identity token when that bearer nears expiry.
func TestConfigureAPIFederationBacksTheStandardClient(t *testing.T) {
	// Under the SDK's 30s mandatory threshold, so every request refreshes.
	fs := newFederationServer(t, 10)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"organization_id":        tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id":     tftypes.NewValue(tftypes.String, "fdrl_developer"),
			"service_account_id":     tftypes.NewValue(tftypes.String, "svac_account"),
			"identity_token_command": stringList(countingTokenScript(t)),
		}),
	}))

	if pd.Client == nil {
		t.Fatal("api_federation did not produce the standard client")
	}
	if pd.OAuthClient != nil || pd.AdminClient != nil {
		t.Error("api_federation alone must not produce the OAuth or admin clients")
	}

	for range 2 {
		if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}

	exchanges, authorized := fs.snapshot()
	if len(exchanges) != 2 {
		t.Fatalf("exchanges = %d, want 2 (one per request, since each token was near expiry)", len(exchanges))
	}
	for i, ex := range exchanges {
		if want := fmt.Sprintf("jwt-%d", i+1); ex["assertion"] != want {
			t.Errorf("exchange %d assertion = %q, want %q", i+1, ex["assertion"], want)
		}
		if ex["federation_rule_id"] != "fdrl_developer" || ex["service_account_id"] != "svac_account" {
			t.Errorf("exchange %d body = %v", i+1, ex)
		}
	}
	want := []string{"Bearer sk-ant-oat01-minted-1", "Bearer sk-ant-oat01-minted-2"}
	if strings.Join(authorized, ",") != strings.Join(want, ",") {
		t.Errorf("authorization headers = %v, want %v", authorized, want)
	}
	for i, headers := range fs.requestHeaders() {
		if headers.Get("X-Api-Key") != "" || headers.Get("anthropic-workspace-id") != "" {
			t.Errorf("request %d sent x-api-key=%q anthropic-workspace-id=%q, want neither", i+1, headers.Get("X-Api-Key"), headers.Get("anthropic-workspace-id"))
		}
	}
}

// TestConfigureBothFederationBlocks: a rule carries a single scope, so a
// pipeline that manages WIF and the workspace holds two rules, and the two
// blocks mint one token each, for their own client.
func TestConfigureBothFederationBlocks(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")

	resp := configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"federation_rule_id":     tftypes.NewValue(tftypes.String, "fdrl_admin"),
			"identity_token_command": stringList(countingTokenScript(t)),
		}),
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"federation_rule_id":     tftypes.NewValue(tftypes.String, "fdrl_developer"),
			"identity_token_command": stringList(countingTokenScript(t)),
		}),
	})
	if w := resp.Diagnostics.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %v, want none with a command per block", w)
	}
	pd := providerDataFrom(t, resp)

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("oauth request failed: %v", err)
	}
	if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("standard request failed: %v", err)
	}
	for range 2 {
		// Cached: neither client re-exchanges while its token has time left.
		if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
			t.Fatalf("standard request failed: %v", err)
		}
	}

	exchanges, authorized := fs.snapshot()
	if len(exchanges) != 2 || exchanges[0]["federation_rule_id"] != "fdrl_admin" || exchanges[1]["federation_rule_id"] != "fdrl_developer" {
		t.Fatalf("exchanges = %v, want one against fdrl_admin then one against fdrl_developer", exchanges)
	}
	want := []string{"Bearer sk-ant-oat01-minted-1", "Bearer sk-ant-oat01-minted-2", "Bearer sk-ant-oat01-minted-2", "Bearer sk-ant-oat01-minted-2"}
	if strings.Join(authorized, ",") != strings.Join(want, ",") {
		t.Errorf("authorization headers = %v, want %v", authorized, want)
	}
	// Neither client carries anything but its bearer: no API key, and no
	// workspace header on a federated token.
	for i, headers := range fs.requestHeaders() {
		if headers.Get("X-Api-Key") != "" || headers.Get("anthropic-workspace-id") != "" {
			t.Errorf("request %d sent x-api-key=%q anthropic-workspace-id=%q, want neither", i+1, headers.Get("X-Api-Key"), headers.Get("anthropic-workspace-id"))
		}
	}
}

// TestConfigureBothFederationBlocksWarnOnSharedIdentitySource: the two
// blocks exchange separately, so one file or literal, typically
// ANTHROPIC_IDENTITY_TOKEN_FILE filling both, presents the same assertion
// twice and the second exchange fails with jti_reused when the token carries
// a jti. A warning at plan time, since a token without jti is fine; a
// command per block is never shared, it fetches a token per exchange.
func TestConfigureBothFederationBlocksWarnOnSharedIdentitySource(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("jwt\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	script := countingTokenScript(t)

	tests := []struct {
		name       string
		env        map[string]string
		federation map[string]tftypes.Value
		api        map[string]tftypes.Value
		warn       bool
	}{
		{
			name: "same file from the environment",
			env:  map[string]string{"ANTHROPIC_IDENTITY_TOKEN_FILE": tokenFile},
			warn: true,
		},
		{
			name: "same literal from the environment",
			env:  map[string]string{"ANTHROPIC_IDENTITY_TOKEN": "jwt"},
			warn: true,
		},
		{
			name:       "same file in both blocks",
			federation: map[string]tftypes.Value{"identity_token_file": tftypes.NewValue(tftypes.String, tokenFile)},
			api:        map[string]tftypes.Value{"identity_token_file": tftypes.NewValue(tftypes.String, tokenFile)},
			warn:       true,
		},
		{
			name:       "different files",
			federation: map[string]tftypes.Value{"identity_token_file": tftypes.NewValue(tftypes.String, tokenFile)},
			api:        map[string]tftypes.Value{"identity_token_file": tftypes.NewValue(tftypes.String, tokenFile+".other")},
		},
		{
			name:       "same command in both blocks",
			federation: map[string]tftypes.Value{"identity_token_command": stringList(script)},
			api:        map[string]tftypes.Value{"identity_token_command": stringList(script)},
		},
		{
			name:       "command beside the environment file",
			env:        map[string]string{"ANTHROPIC_IDENTITY_TOKEN_FILE": tokenFile},
			federation: map[string]tftypes.Value{"identity_token_command": stringList(script)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredentialEnv(t)
			t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			federation := map[string]tftypes.Value{"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_admin")}
			api := map[string]tftypes.Value{"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_developer")}
			for k, v := range tt.federation {
				federation[k] = v
			}
			for k, v := range tt.api {
				api[k] = v
			}

			resp := configureProvider(t, map[string]tftypes.Value{
				"federation":     federationValue(t, federation),
				"api_federation": apiFederationValue(t, api),
			})
			providerDataFrom(t, resp)
			w := resp.Diagnostics.Warnings()
			if got := len(w) == 1 && w[0].Summary() == "Shared Identity Token Source"; got != tt.warn {
				t.Errorf("warnings = %v, want shared-source warning = %v", w, tt.warn)
			}
		})
	}
}

// TestConfigureBothFederationBlocksRejectTheSameRule: a rule grants a single
// scope, so one rule cannot back both clients, and two empty blocks fall
// back to the same ANTHROPIC_FEDERATION_RULE_ID. Caught at plan time rather
// than as a 401 on one client's first request.
func TestConfigureBothFederationBlocksRejectTheSameRule(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_shared")
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt")

	resp := configureProvider(t, map[string]tftypes.Value{
		"federation":     federationValue(t, nil),
		"api_federation": apiFederationValue(t, nil),
	})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Conflicting Federation Rules" ||
		!strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "fdrl_shared") {
		t.Fatalf("diagnostics = %v, want Conflicting Federation Rules naming fdrl_shared", resp.Diagnostics)
	}
}

// TestConfigureAPIFederationFromEnvironment: the SDKs' federation variables
// describe the workload's own rule, which is the one the standard client
// needs, so api_federation = {} is the whole configuration in a job that
// exports them.
func TestConfigureAPIFederationFromEnvironment(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_env")
	t.Setenv("ANTHROPIC_SERVICE_ACCOUNT_ID", "svac_env")
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_env")
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt-from-env")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"api_federation": apiFederationValue(t, nil),
	}))
	if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	exchanges, _ := fs.snapshot()
	if len(exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(exchanges))
	}
	if ex := exchanges[0]; ex["assertion"] != "jwt-from-env" || ex["federation_rule_id"] != "fdrl_env" ||
		ex["service_account_id"] != "svac_env" || ex["workspace_id"] != "wrkspc_env" {
		t.Errorf("exchange body = %v", ex)
	}
}

// TestConfigureAPIFederationConflictsWithAPIKey mirrors the federation and
// auth_token rule: both would fill the standard client. The check reads the
// configuration, so an api_key unknown at plan time conflicts too.
func TestConfigureAPIFederationConflictsWithAPIKey(t *testing.T) {
	for label, apiKey := range map[string]tftypes.Value{
		"known":   tftypes.NewValue(tftypes.String, "sk-ant-api03-configured"),
		"unknown": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	} {
		t.Run(label, func(t *testing.T) {
			clearCredentialEnv(t)

			resp := configureProvider(t, map[string]tftypes.Value{
				"api_key": apiKey,
				"api_federation": apiFederationValue(t, map[string]tftypes.Value{
					"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
					"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_developer"),
					"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
				}),
			})
			if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Conflicting API Credentials" {
				t.Fatalf("diagnostics = %v, want Conflicting API Credentials", resp.Diagnostics)
			}
		})
	}
}

// TestConfigureAPIFederationIgnoresAPIKeyEnvironment: an exported
// ANTHROPIC_API_KEY, the most commonly exported of all the variables, gives
// way to the block with a warning, and never reaches the standard client as
// a second credential.
func TestConfigureAPIFederationIgnoresAPIKeyEnvironment(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-leftover")

	resp := configureProvider(t, map[string]tftypes.Value{
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_developer"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})
	if w := resp.Diagnostics.Warnings(); len(w) != 1 || w[0].Summary() != "ANTHROPIC_API_KEY Ignored" {
		t.Errorf("warnings = %v, want ANTHROPIC_API_KEY Ignored", w)
	}

	pd := providerDataFrom(t, resp)
	if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if _, authorized := fs.snapshot(); len(authorized) != 1 || authorized[0] != "Bearer sk-ant-oat01-minted-1" {
		t.Errorf("authorization headers = %v, want the federated token", authorized)
	}
	if v := fs.requestHeaders()[0].Get("X-Api-Key"); v != "" {
		t.Errorf("standard client also sent x-api-key: %q", v)
	}
}

// TestConfigureAPIFederationWorkspace pins how the provider-level
// workspace_id meets the exchange-time workspace of a federated token. The
// standard client acts in workspace_id whatever credential backs it, so the
// block takes it when it has none of its own; a block attribute naming
// another workspace is an error, not a silent winner; and the header, which
// the SDK states does not apply to federated tokens, is never sent.
func TestConfigureAPIFederationWorkspace(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID tftypes.Value
		block       tftypes.Value
		env         string
		want        string
		wantError   string
	}{
		{
			name:        "provider-level workspace_id scopes the exchange",
			workspaceID: tftypes.NewValue(tftypes.String, "wrkspc_provider"),
			want:        "wrkspc_provider",
		},
		{
			name:  "block workspace_id scopes the exchange",
			block: tftypes.NewValue(tftypes.String, "wrkspc_block"),
			want:  "wrkspc_block",
		},
		{
			name:        "both set to the same workspace",
			workspaceID: tftypes.NewValue(tftypes.String, "wrkspc_same"),
			block:       tftypes.NewValue(tftypes.String, "wrkspc_same"),
			want:        "wrkspc_same",
		},
		{
			name:        "both set to different workspaces",
			workspaceID: tftypes.NewValue(tftypes.String, "wrkspc_provider"),
			block:       tftypes.NewValue(tftypes.String, "wrkspc_block"),
			wantError:   "Conflicting Workspace Configuration",
		},
		{
			// An empty block attribute (a variable with an empty default)
			// is unset, as isSet reads it in the config validators.
			name:        "empty block workspace_id conflicts with nothing",
			workspaceID: tftypes.NewValue(tftypes.String, "wrkspc_provider"),
			block:       tftypes.NewValue(tftypes.String, ""),
			want:        "wrkspc_provider",
		},
		{
			// Configuration over environment, as everywhere else.
			name:        "provider-level workspace_id wins over the environment",
			workspaceID: tftypes.NewValue(tftypes.String, "wrkspc_provider"),
			env:         "wrkspc_env",
			want:        "wrkspc_provider",
		},
		{
			name: "environment alone",
			env:  "wrkspc_env",
			want: "wrkspc_env",
		},
		{
			name: "none",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFederationServer(t, 3600)

			clearCredentialEnv(t)
			t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
			if tt.env != "" {
				t.Setenv("ANTHROPIC_WORKSPACE_ID", tt.env)
			}

			block := map[string]tftypes.Value{
				"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
				"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_developer"),
				"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
			}
			if tt.block.IsKnown() && !tt.block.IsNull() {
				block["workspace_id"] = tt.block
			}
			attrs := map[string]tftypes.Value{"api_federation": apiFederationValue(t, block)}
			if tt.workspaceID.IsKnown() && !tt.workspaceID.IsNull() {
				attrs["workspace_id"] = tt.workspaceID
			}

			resp := configureProvider(t, attrs)
			if tt.wantError != "" {
				if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != tt.wantError {
					t.Fatalf("diagnostics = %v, want %s", resp.Diagnostics, tt.wantError)
				}
				return
			}

			pd := providerDataFrom(t, resp)
			if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
				t.Fatalf("request failed: %v", err)
			}
			exchanges, _ := fs.snapshot()
			if len(exchanges) != 1 {
				t.Fatalf("exchanges = %d, want 1", len(exchanges))
			}
			if got := exchanges[0]["workspace_id"]; got != tt.want {
				t.Errorf("exchange workspace_id = %q, want %q", got, tt.want)
			}
			if v := fs.requestHeaders()[0].Get("anthropic-workspace-id"); v != "" {
				t.Errorf("standard client sent anthropic-workspace-id = %q, want none on a federated token", v)
			}
		})
	}
}

// TestConfigureAPIFederationWorkspaceConflictIsNotAlone: the workspace
// conflict is reported with the block's other diagnostics, so a plan does not
// surface one problem at a time.
func TestConfigureAPIFederationWorkspaceConflictIsNotAlone(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"workspace_id": tftypes.NewValue(tftypes.String, "wrkspc_provider"),
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"workspace_id":   tftypes.NewValue(tftypes.String, "wrkspc_block"),
			"identity_token": tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})
	var summaries []string
	for _, d := range resp.Diagnostics.Errors() {
		summaries = append(summaries, d.Summary())
	}
	got := strings.Join(summaries, ",")
	for _, want := range []string{"Conflicting Workspace Configuration", "Missing Federation Organization", "Missing Federation Rule"} {
		if !strings.Contains(got, want) {
			t.Errorf("errors = %v, want %s among them", summaries, want)
		}
	}
}

// TestFederationWorkspaceIDValidator: the exchange-time workspace is checked
// at plan time like the provider-level one, so a trailing newline fails the
// plan rather than every exchange, and is never read as a workspace different
// from the provider-level ID it otherwise matches. The literal `default`,
// which the exchange accepts for the organization's default workspace, is
// valid, and so is the empty string, which has always read as unset.
func TestFederationWorkspaceIDValidator(t *testing.T) {
	ctx := context.Background()
	for _, block := range []string{"federation", "api_federation"} {
		t.Run(block, func(t *testing.T) {
			schemaResp := &provider.SchemaResponse{}
			(&AnthropicProvider{version: "test"}).Schema(ctx, provider.SchemaRequest{}, schemaResp)
			nested, ok := schemaResp.Schema.Attributes[block].(schema.SingleNestedAttribute)
			if !ok {
				t.Fatalf("%s is %T, want schema.SingleNestedAttribute", block, schemaResp.Schema.Attributes[block])
			}
			attr, ok := nested.Attributes["workspace_id"].(schema.StringAttribute)
			if !ok || len(attr.Validators) == 0 {
				t.Fatalf("%s.workspace_id has no validator", block)
			}

			for value, valid := range map[string]bool{
				"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ":   true,
				"default":                           true,
				"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ\n": false,
				"01JwQvzr7rXLA5AGx3HKfFUJ":          false,
				"":                                  true,
				" ":                                 false,
			} {
				resp := &validator.StringResponse{}
				for _, v := range attr.Validators {
					v.ValidateString(ctx, validator.StringRequest{
						Path:        path.Root(block).AtName("workspace_id"),
						ConfigValue: types.StringValue(value),
					}, resp)
				}
				if got := !resp.Diagnostics.HasError(); got != valid {
					t.Errorf("%s.workspace_id %q: valid = %v, want %v (%v)", block, value, got, valid, resp.Diagnostics)
				}
			}
		})
	}
}

// TestConfigureWorkspaceIDStaysOffTheOAuthExchange: the provider-level
// workspace_id is the standard client's. The org:admin token is
// organization-wide, so the federation block does not take it.
func TestConfigureWorkspaceIDStaysOffTheOAuthExchange(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"workspace_id": tftypes.NewValue(tftypes.String, "wrkspc_provider"),
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_admin"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if exchanges, _ := fs.snapshot(); len(exchanges) != 1 || exchanges[0]["workspace_id"] != "" {
		t.Errorf("exchanges = %v, want one with no workspace", exchanges)
	}
}

// TestConfigureAPIFederationRejectsUnknownAttributes: the per-attribute rule
// of the federation block names the right block in its diagnostics.
func TestConfigureAPIFederationRejectsUnknownAttributes(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt-from-env")

	resp := configureProvider(t, map[string]tftypes.Value{
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_developer"),
		}),
	})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unknown Federation Configuration" ||
		!strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "api_federation.organization_id") {
		t.Errorf("diagnostics = %v, want api_federation.organization_id reported as unknown", resp.Diagnostics)
	}
}

// TestConfigureAPIFederationRequiresOrganizationAndRule: the missing-value
// diagnostics name the block's own attributes, not federation's.
func TestConfigureAPIFederationRequiresOrganizationAndRule(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"api_federation": apiFederationValue(t, map[string]tftypes.Value{
			"identity_token": tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})

	var details []string
	for _, d := range resp.Diagnostics.Errors() {
		details = append(details, d.Detail())
	}
	got := strings.Join(details, "\n")
	if !strings.Contains(got, "api_federation.organization_id") || !strings.Contains(got, "api_federation.federation_rule_id") {
		t.Errorf("errors = %v, want both attributes reported under api_federation", details)
	}
}

// TestConfigureFederationEnvironmentAloneEnablesNeitherBlock: the SDKs'
// federation variables describe exactly what api_federation needs, and are
// exported for other tools in the same job, so without a block the standard
// client stays an API-key client.
func TestConfigureFederationEnvironmentAloneEnablesNeitherBlock(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_env")
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt")

	resp := configureProvider(t, nil)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Missing Credentials" {
		t.Fatalf("diagnostics = %v, want Missing Credentials", resp.Diagnostics)
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "api_federation") {
		t.Errorf("detail %q does not offer api_federation", detail)
	}
}
