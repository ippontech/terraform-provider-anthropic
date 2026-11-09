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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const (
	testOrg      = "00000000-0000-0000-0000-000000000000"
	otherTestOrg = "11111111-1111-1111-1111-111111111111"
)

// cliServer stands in for the Anthropic API: it answers refresh grants with a
// numbered access token, and records the headers of every other request.
type cliServer struct {
	*httptest.Server

	mu        sync.Mutex
	refreshes []map[string]string
	headers   []http.Header
}

func newCLIServer(t *testing.T) *cliServer {
	t.Helper()

	cs := &cliServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		defer cs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/oauth/token" {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			cs.refreshes = append(cs.refreshes, body)
			_, _ = fmt.Fprintf(w, `{"access_token":"sk-ant-oat01-refreshed-%d","refresh_token":"rt-%d","token_type":"Bearer","expires_in":3600}`,
				len(cs.refreshes), len(cs.refreshes))
			return
		}
		cs.headers = append(cs.headers, r.Header.Clone())
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *cliServer) snapshot() ([]map[string]string, []http.Header) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return append([]map[string]string(nil), cs.refreshes...), append([]http.Header(nil), cs.headers...)
}

// cliConfigDir points the SDK at an empty config directory for this test.
func cliConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("ANTHROPIC_CONFIG_DIR", dir)
	return dir
}

// testLogin describes a profile as `ant auth login` would leave it. A zero
// token means the profile is logged out (no credentials file).
type testLogin struct {
	org, workspace, scope, clientID string
	token, refreshToken             string
	expiresAt                       time.Time
	baseURL                         string
}

func writeCLIProfile(t *testing.T, dir, name string, l testLogin) {
	t.Helper()

	auth := map[string]any{"type": "user_oauth", "scope": l.scope}
	if l.clientID != "" {
		auth["client_id"] = l.clientID
	}
	cfg := map[string]any{"version": "1.0", "authentication": auth, "workspace_id": l.workspace}
	if l.baseURL != "" {
		cfg["base_url"] = l.baseURL
	}
	writeJSON(t, filepath.Join(dir, "configs", name+".json"), cfg)

	if l.token == "" {
		return
	}
	creds := map[string]any{
		"version": "1.0", "type": "oauth_token", "access_token": l.token,
		"scope": l.scope, "organization_uuid": l.org, "workspace_id": l.workspace,
	}
	if l.refreshToken != "" {
		creds["refresh_token"] = l.refreshToken
	}
	if !l.expiresAt.IsZero() {
		creds["expires_at"] = l.expiresAt.Unix()
	}
	writeJSON(t, filepath.Join(dir, "credentials", name+".json"), creds)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// cliValue builds the cli block from attrs, every other attribute null.
func cliValue(t *testing.T, attrs map[string]string) tftypes.Value {
	t.Helper()

	ctx := context.Background()
	p := &AnthropicProvider{}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object).AttributeTypes["cli"].(tftypes.Object)

	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		if v, found := attrs[name]; found {
			values[name] = tftypes.NewValue(tftypes.String, v)
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}
	return tftypes.NewValue(objType, values)
}

func adminLogin(org, token string) testLogin {
	return testLogin{org: org, workspace: "wrkspc_default", scope: "org:admin", token: token, expiresAt: time.Now().Add(time.Hour)}
}

// cliError runs Configure with the cli block and returns its one error detail.
func cliError(t *testing.T, attrs map[string]string) string {
	t.Helper()

	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, attrs)})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Configure to fail")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Unusable Anthropic CLI Profile" {
		t.Fatalf("summary = %q, want Unusable Anthropic CLI Profile", got)
	}
	return resp.Diagnostics.Errors()[0].Detail()
}

func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()

	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not contain %q", got, w)
		}
	}
}

// TestConfigureCLIUsesNamedProfile covers the operator's workstation: the
// OAuth client sends the profile's token, and not the profile's workspace,
// which an org:admin token is not bound to.
func TestConfigureCLIUsesNamedProfile(t *testing.T) {
	cs := newCLIServer(t)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", cs.URL)
	writeCLIProfile(t, cliConfigDir(t), "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"cli": cliValue(t, map[string]string{"profile": "admin"}),
	}))
	if pd.Client != nil || pd.AdminClient != nil {
		t.Error("cli alone must not produce the API-key clients")
	}
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	_, headers := cs.snapshot()
	if got := headers[0].Get("Authorization"); got != "Bearer sk-ant-oat01-admin" {
		t.Errorf("authorization = %q, want the profile's token", got)
	}
	if got := headers[0].Get("anthropic-workspace-id"); got != "" {
		t.Errorf("anthropic-workspace-id = %q, want none", got)
	}
}

func TestConfigureCLIProfileFromEnvironment(t *testing.T) {
	cs := newCLIServer(t)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", cs.URL)
	dir := cliConfigDir(t)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	writeCLIProfile(t, dir, "admin-2", adminLogin(testOrg, "sk-ant-oat01-admin-2"))
	t.Setenv("ANTHROPIC_PROFILE", "admin-2")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, nil)}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if _, headers := cs.snapshot(); headers[0].Get("Authorization") != "Bearer sk-ant-oat01-admin-2" {
		t.Errorf("authorization = %q, want admin-2's token", headers[0].Get("Authorization"))
	}
}

// TestConfigureCLIRefreshesExpiredLogin is why the block uses the SDK's
// profile support rather than a token printed by `ant`: an apply that
// outlives the access token keeps going on the refresh token.
func TestConfigureCLIRefreshesExpiredLogin(t *testing.T) {
	cs := newCLIServer(t)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", cs.URL)
	dir := cliConfigDir(t)
	login := adminLogin(testOrg, "sk-ant-oat01-stale")
	login.clientID, login.refreshToken, login.expiresAt = "client", "rt-0", time.Now().Add(-time.Minute)
	writeCLIProfile(t, dir, "admin", login)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"cli": cliValue(t, map[string]string{"profile": "admin"}),
	}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	refreshes, headers := cs.snapshot()
	if len(refreshes) != 1 || refreshes[0]["refresh_token"] != "rt-0" || refreshes[0]["client_id"] != "client" {
		t.Fatalf("refreshes = %v, want one grant with rt-0", refreshes)
	}
	if got := headers[0].Get("Authorization"); got != "Bearer sk-ant-oat01-refreshed-1" {
		t.Errorf("authorization = %q, want the refreshed token", got)
	}
	// The SDK writes the rotated tokens back, so `ant` keeps working too.
	data, err := os.ReadFile(filepath.Join(dir, "credentials", "admin.json"))
	if err != nil || !strings.Contains(string(data), "rt-1") {
		t.Errorf("credentials file not rotated: %s (%v)", data, err)
	}
}

// TestConfigureCLIFindsProfileByOrganization covers the layout the ant docs
// recommend: an everyday workspace profile beside an org:admin one, in the
// same organization. Only the org:admin profile for the organization matches.
func TestConfigureCLIFindsProfileByOrganization(t *testing.T) {
	cs := newCLIServer(t)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", cs.URL)
	dir := cliConfigDir(t)
	everyday := adminLogin(testOrg, "sk-ant-oat01-everyday")
	everyday.scope = "user:inference"
	writeCLIProfile(t, dir, "default", everyday)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	writeCLIProfile(t, dir, "other-org", adminLogin(otherTestOrg, "sk-ant-oat01-other"))
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", testOrg)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, nil)}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if _, headers := cs.snapshot(); headers[0].Get("Authorization") != "Bearer sk-ant-oat01-admin" {
		t.Errorf("authorization = %q, want the admin profile's token", headers[0].Get("Authorization"))
	}
}

func TestConfigureCLIRejectsAmbiguousProfiles(t *testing.T) {
	clearCredentialEnv(t)
	dir := cliConfigDir(t)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	writeCLIProfile(t, dir, "admin-2", adminLogin(testOrg, "sk-ant-oat01-admin-2"))

	wantContains(t, cliError(t, map[string]string{"organization_id": testOrg}), "admin, admin-2", "cli.profile")
}

func TestConfigureCLIRequiresAMatchingProfile(t *testing.T) {
	clearCredentialEnv(t)
	writeCLIProfile(t, cliConfigDir(t), "other-org", adminLogin(otherTestOrg, "sk-ant-oat01-other"))

	wantContains(t, cliError(t, map[string]string{"organization_id": testOrg}), testOrg, "ant auth login --profile admin --scope org:admin")
}

func TestConfigureCLIRequiresAProfileOrOrganization(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)

	wantContains(t, cliError(t, nil), "cli.profile", "ANTHROPIC_ORGANIZATION_ID")
}

func TestConfigureCLIRejectsMissingProfile(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), `"admin" does not exist`, "ant auth login --profile admin")
}

func TestConfigureCLIRejectsLoggedOutProfile(t *testing.T) {
	clearCredentialEnv(t)
	writeCLIProfile(t, cliConfigDir(t), "admin", testLogin{org: testOrg, scope: "org:admin"})

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), "not logged in", "ant auth login --profile admin")
}

func TestConfigureCLIRejectsExpiredLoginWithoutRefresh(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-stale")
	login.expiresAt = time.Now().Add(-time.Minute)
	writeCLIProfile(t, cliConfigDir(t), "admin", login)

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), "expired", "ant auth login --profile admin")
}

func TestConfigureCLIRejectsProfileForAnotherOrganization(t *testing.T) {
	clearCredentialEnv(t)
	writeCLIProfile(t, cliConfigDir(t), "admin", adminLogin(otherTestOrg, "sk-ant-oat01-other"))

	wantContains(t, cliError(t, map[string]string{"profile": "admin", "organization_id": testOrg}), otherTestOrg)
}

// TestConfigureCLIRejectsProfileBaseURL: a profile pointed at another API host
// must not redirect Terraform there without the configuration showing it.
func TestConfigureCLIRejectsProfileBaseURL(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.baseURL = "https://staging.example.invalid"
	writeCLIProfile(t, cliConfigDir(t), "admin", login)

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), "staging.example.invalid", "ANTHROPIC_BASE_URL")
}

func TestConfigureCLIWarnsWithoutAdminScope(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-everyday")
	login.scope = "user:inference"
	writeCLIProfile(t, cliConfigDir(t), "default", login)

	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, map[string]string{"profile": "default"})})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
	if w := resp.Diagnostics.Warnings(); len(w) != 1 || w[0].Summary() != "Anthropic CLI Profile Lacks org:admin" {
		t.Errorf("warnings = %v, want the missing-scope warning", w)
	}
}

// TestConfigureCLIDefersToAuthToken is the fallback contract: CI supplies a
// token, and the cli block in the same configuration is never evaluated, so a
// runner without any profile (or with a stray one) is unaffected.
func TestConfigureCLIDefersToAuthToken(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-ci")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"cli": cliValue(t, map[string]string{"profile": "does-not-exist"}),
	}))
	if pd.OAuthClient == nil {
		t.Fatal("auth_token did not produce an OAuth client")
	}
}

func TestConfigureCLIDefersToFederation(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)

	providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"cli": cliValue(t, map[string]string{"profile": "does-not-exist"}),
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, testOrg),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	}))
}

// TestConfigureCLIDefersToUnknownAuthToken: an auth_token unknown at plan
// time is still configured, so a runner without profiles must not fail on the
// cli block in the same configuration.
func TestConfigureCLIDefersToUnknownAuthToken(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"api_key":    tftypes.NewValue(tftypes.String, "sk-ant-api03-test"),
		"auth_token": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"cli":        cliValue(t, map[string]string{"profile": "does-not-exist"}),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
}

// TestConfigureCLIRejectsUnknownAttributes: an unknown attribute would
// otherwise fall back to the environment and pick another profile.
func TestConfigureCLIRejectsUnknownAttributes(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)
	t.Setenv("ANTHROPIC_PROFILE", "admin")

	cli := cliValue(t, nil)
	var attrs map[string]tftypes.Value
	if err := cli.As(&attrs); err != nil {
		t.Fatalf("cli value: %v", err)
	}
	attrs["profile"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)

	resp := configureProvider(t, map[string]tftypes.Value{"cli": tftypes.NewValue(cli.Type(), attrs)})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Configure to fail")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Unknown CLI Configuration" {
		t.Errorf("summary = %q, want Unknown CLI Configuration", got)
	}
}

// TestConfigureCLIRejectsEnvironmentProfileWithoutAdminScope: an exported
// ANTHROPIC_PROFILE is usually the everyday login. Only a profile the
// configuration names gets away with a warning.
func TestConfigureCLIRejectsEnvironmentProfileWithoutAdminScope(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-everyday")
	login.scope = "user:inference"
	writeCLIProfile(t, cliConfigDir(t), "default", login)
	t.Setenv("ANTHROPIC_PROFILE", "default")

	wantContains(t, cliError(t, map[string]string{"organization_id": testOrg}), "ANTHROPIC_PROFILE", "user:inference")
}

// TestConfigureCLISkipsUnusableCandidates: a stale login beside a working one
// must not make discovery ambiguous.
func TestConfigureCLISkipsUnusableCandidates(t *testing.T) {
	cs := newCLIServer(t)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", cs.URL)
	dir := cliConfigDir(t)
	stale := adminLogin(testOrg, "sk-ant-oat01-stale")
	stale.expiresAt = time.Now().Add(-time.Hour)
	writeCLIProfile(t, dir, "admin", stale)
	writeCLIProfile(t, dir, "admin-2", adminLogin(testOrg, "sk-ant-oat01-admin-2"))

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"cli": cliValue(t, map[string]string{"organization_id": testOrg}),
	}))
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if _, headers := cs.snapshot(); headers[0].Get("Authorization") != "Bearer sk-ant-oat01-admin-2" {
		t.Errorf("authorization = %q, want admin-2's token", headers[0].Get("Authorization"))
	}
}

// TestConfigureCLIReportsOnlyUnusableCandidates: with no usable login left,
// the error says why the candidates were rejected.
func TestConfigureCLIReportsOnlyUnusableCandidates(t *testing.T) {
	clearCredentialEnv(t)
	stale := adminLogin(testOrg, "sk-ant-oat01-stale")
	stale.expiresAt = time.Now().Add(-time.Hour)
	writeCLIProfile(t, cliConfigDir(t), "admin", stale)

	wantContains(t, cliError(t, map[string]string{"organization_id": testOrg}), "no usable org:admin profile", `"admin"`, "cannot be refreshed")
}

// TestConfigureCLIRejectsClientIDWithoutRefreshToken: the SDK refuses this
// shape on every request, so Configure must refuse it first.
func TestConfigureCLIRejectsClientIDWithoutRefreshToken(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.clientID = "client"
	writeCLIProfile(t, cliConfigDir(t), "admin", login)

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), "no refresh token", "ant auth login --profile admin")
}

// TestConfigureCLIRejectsLoginInsideRefreshThreshold: the SDK treats a token
// with less than 30s left as expired.
func TestConfigureCLIRejectsLoginInsideRefreshThreshold(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.expiresAt = time.Now().Add(20 * time.Second)
	writeCLIProfile(t, cliConfigDir(t), "admin", login)

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), "cannot be refreshed")
}

// TestConfigureCLIRejectsProfileNameOutsideTheDirectory: names are validated
// as the SDK's LoadProfile does, before any path is built.
func TestConfigureCLIRejectsProfileNameOutsideTheDirectory(t *testing.T) {
	clearCredentialEnv(t)
	cliConfigDir(t)

	for _, name := range []string{"../admin", `..\admin`, "..", "."} {
		wantContains(t, cliError(t, map[string]string{"profile": name}), "may contain only")
	}
}

// TestConfigureCLIBaseURLMismatchHidesPassword: the base-URL mismatch
// diagnostic names the provider's URL, so the user information of
// ANTHROPIC_BASE_URL must be out of it, as it is out of every other error
// (#553). The comparison ignores it too: the bearer client never sends it.
func TestConfigureCLIBaseURLMismatchHidesPassword(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.baseURL = "https://staging.example.invalid"
	writeCLIProfile(t, cliConfigDir(t), "admin", login)
	t.Setenv("ANTHROPIC_BASE_URL", "https://user:hunter2@gateway.example.invalid")

	detail := cliError(t, map[string]string{"profile": "admin"})
	wantContains(t, detail, "staging.example.invalid", "https://gateway.example.invalid")
	if strings.Contains(detail, "hunter2") || strings.Contains(detail, "user:") {
		t.Errorf("diagnostic leaks the base URL's user information: %q", detail)
	}

	login.baseURL = "https://gateway.example.invalid/"
	writeCLIProfile(t, os.Getenv("ANTHROPIC_CONFIG_DIR"), "admin", login)
	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, map[string]string{"profile": "admin"})})
	if resp.Diagnostics.HasError() {
		t.Fatalf("a profile for the same host without user information must be accepted: %v", resp.Diagnostics)
	}
}

// TestConfigureCLINamedProfileIgnoresOrganizationEnvironment: an
// ANTHROPIC_ORGANIZATION_ID left over from a federation setup must not reject
// the profile the configuration names.
func TestConfigureCLINamedProfileIgnoresOrganizationEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	writeCLIProfile(t, cliConfigDir(t), "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", otherTestOrg)

	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, map[string]string{"profile": "admin"})})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
}

// TestConfigureCLIAcceptsProductionBaseURL covers the unset
// ANTHROPIC_BASE_URL branch: a profile that names the production URL is
// accepted, and defaultBaseURL is where newSDKClient sends requests.
func TestConfigureCLIAcceptsProductionBaseURL(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.baseURL = defaultBaseURL + "/"
	writeCLIProfile(t, cliConfigDir(t), "admin", login)

	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, map[string]string{"profile": "admin"})})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}

	var sent string
	errStop := fmt.Errorf("stop")
	client := newSDKClient(true, option.WithAPIKey("unused"), option.WithMaxRetries(0),
		option.WithMiddleware(func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
			sent = r.URL.Scheme + "://" + r.URL.Host
			return nil, errStop
		}))
	_ = client.Get(context.Background(), "/v1/models", nil, nil)
	if sent != defaultBaseURL {
		t.Errorf("default client sent to %q, want defaultBaseURL %q", sent, defaultBaseURL)
	}
}

// TestConfigureCLIRejectsCredentialsReadableByOthers: the SDK refuses, on
// every request, a credentials file readable by group or other. Configure
// must say so first, with the chmod to run.
func TestConfigureCLIRejectsCredentialsReadableByOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SDK skips the permission check on Windows")
	}
	clearCredentialEnv(t)
	dir := cliConfigDir(t)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	credsPath := filepath.Join(dir, "credentials", "admin.json")
	if err := os.Chmod(credsPath, 0o644); err != nil {
		t.Fatal(err)
	}

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), `"admin"`, "permissions 0644", "chmod 600 "+credsPath)
}

// TestConfigureCLIRejectsSymlinkedCredentials: the SDK refuses a symlinked
// credentials file on every request, so Configure must refuse it first.
func TestConfigureCLIRejectsSymlinkedCredentials(t *testing.T) {
	clearCredentialEnv(t)
	dir := cliConfigDir(t)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	credsPath := filepath.Join(dir, "credentials", "admin.json")
	realPath := filepath.Join(dir, "credentials", "admin.real.json")
	if err := os.Rename(credsPath, realPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, credsPath); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	wantContains(t, cliError(t, map[string]string{"profile": "admin"}), `"admin"`, "is a symlink")
}

// TestConfigureCLIUnparsableBaseURLHidesPassword: an ANTHROPIC_BASE_URL that
// url.Parse rejects cannot have its user information stripped, so it must be
// refused as invalid before the profile's base_url is compared with it and
// quoted in the mismatch diagnostic (#553).
func TestConfigureCLIUnparsableBaseURLHidesPassword(t *testing.T) {
	clearCredentialEnv(t)
	login := adminLogin(testOrg, "sk-ant-oat01-admin")
	login.baseURL = "https://staging.example.invalid"
	writeCLIProfile(t, cliConfigDir(t), "admin", login)
	t.Setenv("ANTHROPIC_BASE_URL", "https://user:hunter2@gateway.example.invalid:80x")

	resp := configureProvider(t, map[string]tftypes.Value{"cli": cliValue(t, map[string]string{"profile": "admin"})})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Configure to fail")
	}
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Detail(), "hunter2") || strings.Contains(d.Detail(), "user:") {
			t.Errorf("diagnostic leaks the base URL's user information: %q", d.Detail())
		}
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Invalid Base URL" {
		t.Errorf("summary = %q, want Invalid Base URL", got)
	}
}

// TestConfigureCLIReportsUnreadableOnlyCandidate: a profile that cannot be
// read is set aside, and named with the reason when nothing else matches,
// instead of the error claiming that no profile exists.
func TestConfigureCLIReportsUnreadableOnlyCandidate(t *testing.T) {
	clearCredentialEnv(t)
	dir := cliConfigDir(t)
	writeCLIProfile(t, dir, "admin", adminLogin(testOrg, "sk-ant-oat01-admin"))
	if err := os.WriteFile(filepath.Join(dir, "configs", "admin.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	wantContains(t, cliError(t, map[string]string{"organization_id": testOrg}), "no usable org:admin profile", `parse profile "admin"`)
}
