// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// clearCredentialEnv unsets every credential the provider reads, so a test only
// sees the environment it sets itself. The SDK's own credential chain reads the
// same variables, so leaking one would change which headers a client sends.
func clearCredentialEnv(t *testing.T) {
	t.Helper()

	for _, k := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_ADMIN_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
	} {
		t.Setenv(k, "")
	}
}

// configureProvider runs Configure against a config holding attrs, with every
// other provider attribute null.
func configureProvider(t *testing.T, attrs map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()

	ctx := context.Background()
	p := &AnthropicProvider{version: "test"}

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
// runs, so without an explicit WithHeaderDel each client would present both
// credentials. The WIF endpoints reject an API key outright, so the stray
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
