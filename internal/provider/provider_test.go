// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// clearCredentialEnv unsets every variable the provider reads, so a test only
// sees the environment it sets itself. A leaked credential would change which
// headers a client sends, and a leaked base URL would send the request
// somewhere the test does not control.
//
// The variables are genuinely unset, not set to "": ANTHROPIC_BASE_URL is only
// skipped when it is empty, so an exported-but-empty value is a third state
// that neither the provider nor a future test should have to reason about.
// t.Setenv runs first purely for the cleanup it registers, which restores the
// original value (or its absence) at the end of the test.
func clearCredentialEnv(t *testing.T) {
	t.Helper()

	for _, k := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_ADMIN_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_ORGANIZATION_ID",
		"ANTHROPIC_FEDERATION_RULE_ID",
		"ANTHROPIC_SERVICE_ACCOUNT_ID",
		"ANTHROPIC_WORKSPACE_ID",
		"ANTHROPIC_IDENTITY_TOKEN",
		"ANTHROPIC_IDENTITY_TOKEN_FILE",
	} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

// configureProvider runs Configure against a config holding attrs, with every
// other provider attribute null, on a provider built by New with opts.
func configureProvider(t *testing.T, attrs map[string]tftypes.Value, opts ...Option) *provider.ConfigureResponse {
	t.Helper()
	p, ok := New("test", opts...)().(*AnthropicProvider)
	if !ok {
		t.Fatal("New does not build an *AnthropicProvider")
	}
	return configureProviderInstance(t, p, attrs)
}

// configureProviderInstance is configureProvider on a provider the test keeps,
// to call its resource and data source factories afterwards.
func configureProviderInstance(t *testing.T, p *AnthropicProvider, attrs map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()

	ctx := context.Background()

	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", schemaResp.Diagnostics)
	}

	objType, ok := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("provider schema is not an object type")
	}

	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		if v, found := attrs[name]; found {
			values[name] = v
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}

	resp := &provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, values)},
	}, resp)

	return resp
}

func providerDataFrom(t *testing.T, resp *provider.ConfigureResponse) *providerdata.ProviderData {
	t.Helper()

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
	pd, ok := resp.ResourceData.(*providerdata.ProviderData)
	if !ok {
		t.Fatalf("ResourceData is %T, want *providerdata.ProviderData", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("DataSourceData and ResourceData must be the same ProviderData")
	}
	return pd
}

func TestConfigureRequiresAtLeastOneCredential(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, nil)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when no credential is configured")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Missing Credentials" {
		t.Errorf("summary = %q, want %q", got, "Missing Credentials")
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "auth_token") {
		t.Errorf("detail %q does not offer auth_token as a third option", detail)
	}
}

// TestConfigureAuthTokenAloneIsSufficient covers the WIF-only setup: an
// operator managing federation resources has no API key at all.
func TestConfigureAuthTokenAloneIsSufficient(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"auth_token": tftypes.NewValue(tftypes.String, "sk-ant-oat01-config"),
	})
	pd := providerDataFrom(t, resp)

	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil, want a client built from auth_token")
	}
	if pd.Client != nil {
		t.Error("Client should be nil when api_key is not configured")
	}
	if pd.AdminClient != nil {
		t.Error("AdminClient should be nil when admin_api_key is not configured")
	}
}

func TestConfigureAuthTokenFromEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")

	pd := providerDataFrom(t, configureProvider(t, nil))

	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil, want a client built from ANTHROPIC_AUTH_TOKEN")
	}
}

func TestConfigureBuildsEveryConfiguredClient(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"api_key":       tftypes.NewValue(tftypes.String, "sk-ant-api03-x"),
		"admin_api_key": tftypes.NewValue(tftypes.String, "sk-ant-admin03-x"),
		"auth_token":    tftypes.NewValue(tftypes.String, "sk-ant-oat01-x"),
	})
	pd := providerDataFrom(t, resp)

	if pd.Client == nil {
		t.Error("Client is nil")
	}
	if pd.AdminClient == nil {
		t.Error("AdminClient is nil")
	}
	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil")
	}
}

// TestConfigureClientsCarryExactlyOneCredential is the regression test for the
// SDK's default credential chain: anthropic.NewClient prepends options derived
// from ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN before our explicit option
// runs, so without option.WithoutEnvironmentDefaults each client would present
// both credentials. The WIF endpoints reject an API key outright, so the stray
// header is not cosmetic.
func TestConfigureClientsCarryExactlyOneCredential(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")

	pd := providerDataFrom(t, configureProvider(t, nil))

	t.Run("standard client sends only x-api-key", func(t *testing.T) {
		if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if got.Get("X-Api-Key") != "sk-ant-api03-env" {
			t.Errorf("x-api-key = %q, want the API key", got.Get("X-Api-Key"))
		}
		if v := got.Get("Authorization"); v != "" {
			t.Errorf("standard client also sent Authorization: %q", v)
		}
	})

	t.Run("oauth client sends only the bearer token", func(t *testing.T) {
		if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if got.Get("Authorization") != "Bearer sk-ant-oat01-env" {
			t.Errorf("authorization = %q, want the bearer token", got.Get("Authorization"))
		}
		if v := got.Get("X-Api-Key"); v != "" {
			t.Errorf("oauth client also sent x-api-key: %q", v)
		}
	})
}

// TestConfigureWorkspaceIDHeader: workspace_id scopes the standard client
// alone. An identity-linked key that is not bound to one workspace is
// rejected without the header, while the OAuth client must not carry it: a
// federated bearer selects its workspace at exchange time.
func TestConfigureWorkspaceIDHeader(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]tftypes.Value
		env    string
		want   string
	}{
		{
			name:   "config",
			config: map[string]tftypes.Value{"workspace_id": tftypes.NewValue(tftypes.String, "wrkspc_config")},
			want:   "wrkspc_config",
		},
		{
			// The SDKs' federation variable: exported by other tools and
			// by the WIF guide, and fatal to a single-workspace key, so it
			// never reaches the standard client on its own.
			name: "environment ignored",
			env:  "wrkspc_env",
			want: "",
		},
		{
			name:   "config with environment set",
			config: map[string]tftypes.Value{"workspace_id": tftypes.NewValue(tftypes.String, "wrkspc_config")},
			env:    "wrkspc_env",
			want:   "wrkspc_config",
		},
		{
			name: "neither",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			clearCredentialEnv(t)
			t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")
			if tt.env != "" {
				t.Setenv("ANTHROPIC_WORKSPACE_ID", tt.env)
			}

			pd := providerDataFrom(t, configureProvider(t, tt.config))

			if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if v := got.Get("anthropic-workspace-id"); v != tt.want {
				t.Errorf("standard client anthropic-workspace-id = %q, want %q", v, tt.want)
			}

			if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if v := got.Get("anthropic-workspace-id"); v != "" {
				t.Errorf("oauth client sent anthropic-workspace-id = %q, want none", v)
			}
		})
	}
}

// TestWorkspaceIDValidator: the format is checked at plan time, so a trailing
// newline (file() without trimspace) or a non-workspace ID never reaches the
// header, where net/http would fail every request of the standard client.
func TestWorkspaceIDValidator(t *testing.T) {
	ctx := context.Background()
	schemaResp := &provider.SchemaResponse{}
	(&AnthropicProvider{version: "test"}).Schema(ctx, provider.SchemaRequest{}, schemaResp)
	attr, ok := schemaResp.Schema.Attributes["workspace_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("workspace_id is %T, want schema.StringAttribute", schemaResp.Schema.Attributes["workspace_id"])
	}
	if len(attr.Validators) == 0 {
		t.Fatal("workspace_id has no validator")
	}

	for value, valid := range map[string]bool{
		"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ":   true,
		"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ\n": false,
		" wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ":  false,
		"01JwQvzr7rXLA5AGx3HKfFUJ":          false,
		"wrkspc_":                           false,
		"":                                  false,
	} {
		resp := &validator.StringResponse{}
		for _, v := range attr.Validators {
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("workspace_id"),
				ConfigValue: types.StringValue(value),
			}, resp)
		}
		if got := !resp.Diagnostics.HasError(); got != valid {
			t.Errorf("workspace_id %q: valid = %v, want %v (%v)", value, got, valid, resp.Diagnostics)
		}
	}
}

// TestConfigureRejectsUnknownWorkspaceID: same rule as the federation block.
// Read as unset, an unknown workspace_id would send no header and route the
// plan's reads to the key's own workspace.
func TestConfigureRejectsUnknownWorkspaceID(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env")

	resp := configureProvider(t, map[string]tftypes.Value{
		"workspace_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure succeeded, want an error for an unknown workspace_id")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Unknown Workspace Configuration" {
		t.Errorf("summary = %q, want Unknown Workspace Configuration", got)
	}
}

// TestConfigureStandardClientDropsInheritedBearer covers the mirror case of
// TestConfigureClientsCarryExactlyOneCredential. The SDK's chain stops at the
// first credential it finds, so ANTHROPIC_API_KEY masks the bearer token; only
// an api_key supplied through config with just ANTHROPIC_AUTH_TOKEN exported
// lets the chain contribute an Authorization header to the standard client.
func TestConfigureStandardClientDropsInheritedBearer(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"api_key": tftypes.NewValue(tftypes.String, "sk-ant-api03-config"),
	}))

	if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got.Get("X-Api-Key") != "sk-ant-api03-config" {
		t.Errorf("x-api-key = %q, want the configured API key", got.Get("X-Api-Key"))
	}
	if v := got.Get("Authorization"); v != "" {
		t.Errorf("standard client also sent Authorization: %q", v)
	}
}

// TestConfigureIgnoresTheAmbientProfile covers the credential sources beyond
// the two env vars. `ant auth login --profile admin` — the very command the
// provider docs tell operators to run — writes a profile file and makes it the
// active one, and the SDK's chain reaches it whenever no credential variable
// is exported. option.WithConfig then applies that profile's non-credential
// settings unconditionally, so its workspace_id would be stamped on every
// request (and its base_url would win over the production default) even though
// the Terraform configuration named a credential explicitly and never
// mentioned a workspace.
func TestConfigureIgnoresTheAmbientProfile(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	clearCredentialEnv(t)
	writeProfile(t, "admin", `{
		"version": "1.0",
		"authentication": {"type": "user_oauth"},
		"base_url": "https://profile.example.invalid",
		"workspace_id": "wrkspc_from_the_ambient_profile"
	}`)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"auth_token": tftypes.NewValue(tftypes.String, "sk-ant-oat01-config"),
	}))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if v := got.Get("anthropic-workspace-id"); v != "" {
		t.Errorf("client inherited the profile's workspace scoping: anthropic-workspace-id = %q", v)
	}
	if got.Get("Authorization") != "Bearer sk-ant-oat01-config" {
		t.Errorf("authorization = %q, want the configured bearer token", got.Get("Authorization"))
	}
}

// writeProfile installs a profile file the SDK's credential chain would pick
// up, in a config directory scoped to this test. ANTHROPIC_PROFILE selects it
// explicitly, which is the step-3 source; the fallback active-profile lookup
// (step 5) reads the same file through the same option.WithConfig.
func writeProfile(t *testing.T, name, contents string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "configs"), 0o700); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "configs", name+".json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	t.Setenv("ANTHROPIC_CONFIG_DIR", dir)
	t.Setenv("ANTHROPIC_PROFILE", name)
}

func TestResolveCredential(t *testing.T) {
	const envVar = "ANTHROPIC_TEST_CREDENTIAL"

	tests := []struct {
		name        string
		configValue types.String
		env         string
		want        string
	}{
		{
			name:        "config wins over environment",
			configValue: types.StringValue("from-config"),
			env:         "from-env",
			want:        "from-config",
		},
		{
			name:        "null config falls back to environment",
			configValue: types.StringNull(),
			env:         "from-env",
			want:        "from-env",
		},
		{
			name:        "unknown config falls back to environment",
			configValue: types.StringUnknown(),
			env:         "from-env",
			want:        "from-env",
		},
		{
			name:        "nothing configured yields an empty credential",
			configValue: types.StringNull(),
			env:         "",
			want:        "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVar, tc.env)
			if got := resolveCredential(tc.configValue, envVar); got != tc.want {
				t.Errorf("resolveCredential() = %q, want %q", got, tc.want)
			}
		})
	}
}
