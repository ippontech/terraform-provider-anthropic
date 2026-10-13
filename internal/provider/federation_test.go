// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
// rather than replaying the first one (the exchange rejects a repeated jti,
// which GitHub Actions tokens carry).
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
	// ANTHROPIC_WORKSPACE_ID scopes the exchange, as it does in the SDKs' own
	// federation, whose 401 hint tells the operator to set it.
	if ex := exchanges[0]; ex["assertion"] != "jwt-from-file" || ex["federation_rule_id"] != "fdrl_env" || ex["workspace_id"] != "wrkspc_env" {
		t.Errorf("exchange body = %v", ex)
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

	resp := configureProvider(t, map[string]tftypes.Value{
		"auth_token": tftypes.NewValue(tftypes.String, "sk-ant-oat01-configured"),
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

// TestConfigureFederationIgnoresAuthTokenEnvironment: an exported
// ANTHROPIC_AUTH_TOKEN gives way to the block, as the environment does to
// every provider argument. The SDKs rank it the other way round, hence the
// warning.
func TestConfigureFederationIgnoresAuthTokenEnvironment(t *testing.T) {
	fs := newFederationServer(t, 3600)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-leftover")

	resp := configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})
	if w := resp.Diagnostics.Warnings(); len(w) != 1 || w[0].Summary() != "ANTHROPIC_AUTH_TOKEN Ignored" {
		t.Errorf("warnings = %v, want ANTHROPIC_AUTH_TOKEN Ignored", w)
	}

	pd := providerDataFrom(t, resp)
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if _, authorized := fs.snapshot(); len(authorized) != 1 || authorized[0] != "Bearer sk-ant-oat01-minted-1" {
		t.Errorf("authorization headers = %v, want the federated token", authorized)
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

// TestCommandIdentityTokenKeepsStderrOutOfTheError: the error lands in CI
// logs, and a failing CLI under `set -x` or `curl -v` echoes the token to
// stderr. The error points to the debug log instead.
func TestCommandIdentityTokenKeepsStderrOutOfTheError(t *testing.T) {
	fn := commandIdentityToken([]string{"sh", "-c", "echo 'Authorization: Bearer eyJsecret' >&2; exit 1"}, identityTokenCommandTimeout)

	_, err := fn(context.Background())
	if err == nil || strings.Contains(err.Error(), "eyJsecret") || !strings.Contains(err.Error(), "TF_LOG=DEBUG") {
		t.Errorf("err = %v, want a failure that omits stderr and names the debug log", err)
	}
}

// TestCommandIdentityTokenTimesOutWithChildren covers the documented `sh -c`
// shape: the timeout kills sh, but its children keep stdout open, and only
// the WaitDelay stops Run from waiting on them.
func TestCommandIdentityTokenTimesOutWithChildren(t *testing.T) {
	start := time.Now()
	_, err := commandIdentityToken([]string{"sh", "-c", "sleep 60 | cat"}, 100*time.Millisecond)(context.Background())
	if elapsed := time.Since(start); elapsed > identityTokenCommandWaitDelay+5*time.Second {
		t.Errorf("returned after %s, want the timeout plus WaitDelay", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Errorf("err = %v, want a timeout error", err)
	}
}

// TestConfigureFederationRejectsUnknownAttributes: an attribute unknown at
// plan time is named as such, rather than read as unset, which would fall
// back to the environment or report the value as missing.
func TestConfigureFederationRejectsUnknownAttributes(t *testing.T) {
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	listType := tftypes.List{ElementType: tftypes.String}
	known := map[string]tftypes.Value{
		"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
		"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
	}
	tests := []struct {
		label     string
		attribute string
		value     tftypes.Value
	}{
		{"organization_id", "organization_id", unknown},
		{"command list", "identity_token_command", tftypes.NewValue(listType, tftypes.UnknownValue)},
		{"command element", "identity_token_command", tftypes.NewValue(listType, []tftypes.Value{unknown})},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			clearCredentialEnv(t)
			t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt-from-env")

			attrs := map[string]tftypes.Value{tt.attribute: tt.value}
			for k, v := range known {
				if _, set := attrs[k]; !set {
					attrs[k] = v
				}
			}
			resp := configureProvider(t, map[string]tftypes.Value{"federation": federationValue(t, attrs)})
			if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unknown Federation Configuration" ||
				!strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "federation."+tt.attribute) {
				t.Errorf("diagnostics = %v, want federation.%s reported as unknown", resp.Diagnostics, tt.attribute)
			}
		})
	}
}

func TestConfigureFederationConflictsWithUnknownAuthToken(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"auth_token": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Conflicting OAuth Credentials" {
		t.Fatalf("diagnostics = %v, want Conflicting OAuth Credentials at plan time", resp.Diagnostics)
	}
}

// TestIdentityTokenSourceConfigWinsOverEnvironment: CI exports the SDK's
// identity variables for other tools, and a source written in the block is
// not in conflict with them.
func TestIdentityTokenSourceConfigWinsOverEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN_FILE", "/does/not/exist")

	fn, err := identityTokenSource(context.Background(), federationModel{
		IdentityToken:        types.StringValue("jwt-from-config"),
		IdentityTokenCommand: types.ListNull(types.StringType),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got, _ := fn(context.Background()); got != "jwt-from-config" {
		t.Errorf("token = %q, want the configured literal", got)
	}
}

func TestCommandIdentityTokenRejectsEmptyOutput(t *testing.T) {
	fn := commandIdentityToken([]string{"sh", "-c", "true"}, identityTokenCommandTimeout)

	if _, err := fn(context.Background()); err == nil || !strings.Contains(err.Error(), "printed no token") {
		t.Errorf("err = %v, want an empty-output error", err)
	}
}

// TestCommandIdentityTokenKeepsTokenDespiteLingeringChild: the program printed
// the token and exited, but a child it started keeps stdout open past the
// WaitDelay. The token is whole, so ErrWaitDelay does not discard it.
func TestCommandIdentityTokenKeepsTokenDespiteLingeringChild(t *testing.T) {
	fn := commandIdentityToken([]string{"sh", "-c", "sleep 60 & echo jwt-from-command"}, identityTokenCommandTimeout)

	got, err := fn(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want the token despite the lingering child", err)
	}
	if got != "jwt-from-command" {
		t.Errorf("token = %q, want the command's output", got)
	}
}

// TestCommandIdentityTokenReportsCancellation: a caller cancelling the
// exchange (Ctrl-C) is not a failure of the command, so the error is the
// context's, not a "signal: killed" that points the operator at stderr.
func TestCommandIdentityTokenReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := commandIdentityToken([]string{"sh", "-c", "sleep 60"}, identityTokenCommandTimeout)(ctx)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "TF_LOG") {
		t.Errorf("err = %v, want context.Canceled without a pointer to stderr", err)
	}
}
