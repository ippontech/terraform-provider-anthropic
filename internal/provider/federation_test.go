// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// federationServer stands in for the Anthropic API: it answers the token
// exchange with a numbered access token and records every assertion it is
// sent, and records the Authorization header of every other request.
type federationServer struct {
	*httptest.Server

	expiresIn int

	mu         sync.Mutex
	exchanges  []map[string]string
	authorized []string
}

func newFederationServer(t *testing.T, expiresIn int) *federationServer {
	t.Helper()

	fs := &federationServer{expiresIn: expiresIn}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/oauth/token" {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fs.exchanges = append(fs.exchanges, body)
			_, _ = fmt.Fprintf(w, `{"access_token":"sk-ant-oat01-minted-%d","token_type":"Bearer","expires_in":%d,"scope":"org:admin"}`,
				len(fs.exchanges), fs.expiresIn)
			return
		}
		fs.authorized = append(fs.authorized, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *federationServer) snapshot() ([]map[string]string, []string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]map[string]string(nil), fs.exchanges...), append([]string(nil), fs.authorized...)
}

// federationValue builds the federation block from attrs, every other
// attribute null.
func federationValue(t *testing.T, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()

	ctx := context.Background()
	p := &AnthropicProvider{}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object).AttributeTypes["federation"].(tftypes.Object)

	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		if v, found := attrs[name]; found {
			values[name] = v
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}
	return tftypes.NewValue(objType, values)
}

func stringList(values ...string) tftypes.Value {
	elems := make([]tftypes.Value, len(values))
	for i, v := range values {
		elems[i] = tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, elems)
}

// countingTokenScript writes a script that prints jwt-1, jwt-2, ... on
// successive runs, so a test can tell a fresh identity token from a reused one.
func countingTokenScript(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	script := filepath.Join(dir, "token.sh")
	counter := filepath.Join(dir, "count")
	body := fmt.Sprintf("#!/bin/sh\nn=$(cat %q 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > %q\necho jwt-$n\n", counter, counter)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write token script: %v", err)
	}
	return script
}

// TestConfigureFederationExchangesAndRefreshes covers the point of the
// block: the OAuth client mints its own bearer token, and when that token is
// close to expiry it exchanges again with a freshly fetched identity token
// rather than replaying the first one (single-use assertions such as GitHub
// Actions and Kubernetes tokens would be rejected as a replay).
func TestConfigureFederationExchangesAndRefreshes(t *testing.T) {
	// Under the SDK's 30s mandatory threshold, so every request refreshes.
	fs := newFederationServer(t, 10)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":        tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id":     tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"service_account_id":     tftypes.NewValue(tftypes.String, "svac_account"),
			"identity_token_command": stringList(countingTokenScript(t)),
		}),
	}))

	if pd.OAuthClient == nil || pd.OAuthClient.Client == nil {
		t.Fatal("federation did not produce an OAuth client")
	}
	if pd.Client != nil || pd.AdminClient != nil {
		t.Error("federation alone must not produce the API-key clients")
	}

	for range 2 {
		if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
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
		if ex["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" ||
			ex["federation_rule_id"] != "fdrl_rule" ||
			ex["organization_id"] != "00000000-0000-0000-0000-000000000000" ||
			ex["service_account_id"] != "svac_account" {
			t.Errorf("exchange %d body = %v", i+1, ex)
		}
	}
	want := []string{"Bearer sk-ant-oat01-minted-1", "Bearer sk-ant-oat01-minted-2"}
	if strings.Join(authorized, ",") != strings.Join(want, ",") {
		t.Errorf("authorization headers = %v, want %v", authorized, want)
	}
}

// TestConfigureFederationCachesTheToken checks the other side of refresh: a
// token with time left is reused, so a plan does not run the identity command
// once per resource.
func TestConfigureFederationCachesTheToken(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":        tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id":     tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"identity_token_command": stringList(countingTokenScript(t)),
		}),
	}))

	for range 3 {
		if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}
	if exchanges, _ := fs.snapshot(); len(exchanges) != 1 {
		t.Errorf("exchanges = %d, want 1", len(exchanges))
	}
}

// TestConfigureFederationFromEnvironment covers an empty block filled from
// the variables the Anthropic SDKs read, so CI can configure federation the
// same way for Terraform as for its other Anthropic clients.
func TestConfigureFederationFromEnvironment(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("jwt-from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_env")
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_env")
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN_FILE", tokenFile)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, nil),
	}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	exchanges, _ := fs.snapshot()
	if len(exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(exchanges))
	}
	if ex := exchanges[0]; ex["assertion"] != "jwt-from-file" || ex["federation_rule_id"] != "fdrl_env" {
		t.Errorf("exchange body = %v", ex)
	}
	// ANTHROPIC_WORKSPACE_ID names the workspace a request is sent to; it must
	// not silently become the exchange-time scope of the federated token.
	if ws := exchanges[0]["workspace_id"]; ws != "" {
		t.Errorf("exchange workspace_id = %q, want none from the environment", ws)
	}
}

func TestConfigureFederationWorkspaceFromConfig(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"workspace_id":       tftypes.NewValue(tftypes.String, "wrkspc_config"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if exchanges, _ := fs.snapshot(); len(exchanges) != 1 || exchanges[0]["workspace_id"] != "wrkspc_config" {
		t.Errorf("exchanges = %v, want one scoped to wrkspc_config", exchanges)
	}
}

// TestConfigureFederationEnvironmentAloneDoesNothing pins the opt-in: the
// SDK's federation variables are commonly exported for other tools in the same
// job, and must not switch the provider's credential without a federation block.
func TestConfigureFederationEnvironmentAloneDoesNothing(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "00000000-0000-0000-0000-000000000000")
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_env")
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt")

	resp := configureProvider(t, nil)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Missing Credentials" {
		t.Fatalf("diagnostics = %v, want Missing Credentials", resp.Diagnostics)
	}
}

func TestConfigureFederationConflictsWithAuthToken(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-leftover")

	resp := configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Conflicting OAuth Credentials" {
		t.Fatalf("diagnostics = %v, want Conflicting OAuth Credentials", resp.Diagnostics)
	}
}

func TestConfigureFederationRequiresOrganizationAndRule(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"identity_token": tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})

	var summaries []string
	for _, d := range resp.Diagnostics.Errors() {
		summaries = append(summaries, d.Summary())
	}
	got := strings.Join(summaries, ",")
	if !strings.Contains(got, "Missing Federation Organization") || !strings.Contains(got, "Missing Federation Rule") {
		t.Errorf("errors = %v, want both the organization and the rule reported", summaries)
	}
}

func TestIdentityTokenSourceRejectsTwoSources(t *testing.T) {
	clearCredentialEnv(t)

	_, err := identityTokenSource(context.Background(), federationModel{
		IdentityToken:        types.StringValue("jwt"),
		IdentityTokenFile:    types.StringValue("/tmp/token"),
		IdentityTokenCommand: types.ListNull(types.StringType),
	})
	if err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Errorf("err = %v, want a conflict error", err)
	}
}

func TestIdentityTokenSourceRequiresOne(t *testing.T) {
	clearCredentialEnv(t)

	_, err := identityTokenSource(context.Background(), federationModel{
		IdentityTokenCommand: types.ListNull(types.StringType),
	})
	if err == nil || !strings.Contains(err.Error(), "no identity token") {
		t.Errorf("err = %v, want a missing-source error", err)
	}
}

// TestCommandIdentityTokenReportsStderr: a failing token command (an expired
// `az login`, say) should explain itself in the provider's error.
func TestCommandIdentityTokenReportsStderr(t *testing.T) {
	fn := commandIdentityToken([]string{"sh", "-c", "echo 'please run az login' >&2; exit 1"})

	_, err := fn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "please run az login") {
		t.Errorf("err = %v, want stderr in the message", err)
	}
}

func TestCommandIdentityTokenRejectsEmptyOutput(t *testing.T) {
	fn := commandIdentityToken([]string{"sh", "-c", "true"})

	if _, err := fn(context.Background()); err == nil || !strings.Contains(err.Error(), "printed no token") {
		t.Errorf("err = %v, want an empty-output error", err)
	}
}
