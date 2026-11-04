// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// headerRecorder is an httptest server that keeps the path and credential
// headers of every request it answers.
type headerRecorder struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newHeaderRecorder(t *testing.T) *headerRecorder {
	t.Helper()
	hr := &headerRecorder{}
	hr.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hr.mu.Lock()
		hr.seen = append(hr.seen, r.URL.Path+" x-api-key="+r.Header.Get("X-Api-Key")+" authorization="+r.Header.Get("Authorization"))
		hr.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(hr.Close)
	return hr
}

func (hr *headerRecorder) requests() []string {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	return append([]string(nil), hr.seen...)
}

// TestWithBaseURLPointsEveryClientAtTheServer covers the option the fake-API
// tests rely on: the standard, Admin and OAuth clients all reach the server
// given to WithBaseURL, each with its own credential, and the option wins
// over an exported ANTHROPIC_BASE_URL (here a decoy that must stay unused).
func TestWithBaseURLPointsEveryClientAtTheServer(t *testing.T) {
	fake := newHeaderRecorder(t)
	decoy := newHeaderRecorder(t)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", decoy.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"api_key":       tftypes.NewValue(tftypes.String, "sk-ant-api03-x"),
		"admin_api_key": tftypes.NewValue(tftypes.String, "sk-ant-admin01-x"),
		"auth_token":    tftypes.NewValue(tftypes.String, "sk-ant-oat01-x"),
	}, WithBaseURL(fake.URL)))

	ctx := context.Background()
	if err := pd.Client.Get(ctx, "/v1/models", nil, nil); err != nil {
		t.Fatalf("standard client: %v", err)
	}
	if _, err := pd.AdminClient.DoRequest(ctx, http.MethodGet, "/v1/organizations/me", nil); err != nil {
		t.Fatalf("admin client: %v", err)
	}
	if err := pd.OAuthClient.Get(ctx, "/v1/organizations/service_accounts", nil, nil); err != nil {
		t.Fatalf("oauth client: %v", err)
	}

	want := []string{
		"/v1/models x-api-key=sk-ant-api03-x authorization=",
		"/v1/organizations/me x-api-key=sk-ant-admin01-x authorization=",
		"/v1/organizations/service_accounts x-api-key= authorization=Bearer sk-ant-oat01-x",
	}
	got := fake.requests()
	if len(got) != len(want) {
		t.Fatalf("fake saw %d requests, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
	if n := len(decoy.requests()); n != 0 {
		t.Errorf("ANTHROPIC_BASE_URL received %d requests, want 0", n)
	}
}

// TestWithBaseURLCarriesTheFederationExchange checks that the token exchange
// of the federation block follows the option too, not only the requests
// made with the minted token.
func TestWithBaseURLCarriesTheFederationExchange(t *testing.T) {
	fs := newFederationServer(t, 3600)
	decoy := newHeaderRecorder(t)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", decoy.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"federation": federationValue(t, map[string]tftypes.Value{
			"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
			"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
			"service_account_id": tftypes.NewValue(tftypes.String, "svac_account"),
			"identity_token":     tftypes.NewValue(tftypes.String, "jwt"),
		}),
	}, WithBaseURL(fs.URL)))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/organizations/service_accounts", nil, nil); err != nil {
		t.Fatalf("oauth client: %v", err)
	}
	exchanges, authorized := fs.snapshot()
	if len(exchanges) != 1 || len(authorized) != 1 || authorized[0] != "Bearer sk-ant-oat01-minted-1" {
		t.Errorf("exchanges = %d, authorized = %q; want one exchange and the minted bearer", len(exchanges), authorized)
	}
	if n := len(decoy.requests()); n != 0 {
		t.Errorf("ANTHROPIC_BASE_URL received %d requests, want 0", n)
	}
}

// TestNewWithoutOptionsKeepsTheDefaults pins what users run: main.go calls
// New without options, so no base URL is set and the clients keep the SDK's
// and internal/admin's defaults (or ANTHROPIC_BASE_URL).
func TestNewWithoutOptionsKeepsTheDefaults(t *testing.T) {
	p, ok := New("1.2.3")().(*AnthropicProvider)
	if !ok {
		t.Fatal("New does not build an *AnthropicProvider")
	}
	if p.baseURL != "" || p.version != "1.2.3" {
		t.Errorf("New(\"1.2.3\") = %+v, want version 1.2.3 and no base URL", *p)
	}

	clearCredentialEnv(t)
	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"admin_api_key": tftypes.NewValue(tftypes.String, "sk-ant-admin01-x"),
	}))
	if pd.AdminClient.BaseURL != "https://api.anthropic.com" {
		t.Errorf("admin base URL = %q, want the production default", pd.AdminClient.BaseURL)
	}
}
