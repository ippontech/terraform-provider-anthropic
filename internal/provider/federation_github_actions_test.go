// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// oidcServer stands in for GitHub Actions' OIDC token endpoint: it answers
// each request with a numbered token, jwt-1, jwt-2..., and records the
// requests it was sent.
type oidcServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []*http.Request
}

func newOIDCServer(t *testing.T) *oidcServer {
	t.Helper()

	s := &oidcServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.requests = append(s.requests, r.Clone(context.Background()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"count":%d,"value":"jwt-%d"}`, len(s.requests), len(s.requests))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *oidcServer) snapshot() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...)
}

// requestURL is the shape of the ACTIONS_ID_TOKEN_REQUEST_URL a runner
// exports: it already carries a query string.
func (s *oidcServer) requestURL() string {
	return s.URL + "/_apis/distributedtask/hubs/Actions/plans/plan/jobs/job/idtoken?api-version=2.0"
}

// setGitHubActionsEnv exports what GitHub sets in a job granted
// `permissions: id-token: write`.
func setGitHubActionsEnv(t *testing.T, requestURL string) {
	t.Helper()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", requestURL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
}

// githubActionsValue builds the github_actions attribute from attrs, every
// other attribute null. Its type is read from the federation block, which
// shares its attributes with api_federation.
func githubActionsValue(t *testing.T, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()

	objType := federationValue(t, nil).Type().(tftypes.Object).AttributeTypes["github_actions"].(tftypes.Object)
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

// githubActionsObject is a configured github_actions source, as
// identityTokenSource receives it.
func githubActionsObject(audience types.String) types.Object {
	return types.ObjectValueMust(map[string]attr.Type{"audience": types.StringType}, map[string]attr.Value{"audience": audience})
}

// TestConfigureFederationGitHubActions covers the point of the source, in
// both blocks: every exchange requests a fresh token from GitHub with the
// job's request token and the default audience, so a token with a single-use
// jti is never presented twice. An exported ANTHROPIC_IDENTITY_TOKEN does not
// replace it: a source written in the block wins over the environment.
func TestConfigureFederationGitHubActions(t *testing.T) {
	for _, tt := range []struct {
		block  string
		client func(*providerdata.ProviderData) *anthropic.Client
	}{
		{"federation", func(pd *providerdata.ProviderData) *anthropic.Client { return pd.OAuthClient.Client }},
		{"api_federation", func(pd *providerdata.ProviderData) *anthropic.Client { return pd.Client }},
	} {
		t.Run(tt.block, func(t *testing.T) {
			oidc := newOIDCServer(t)
			// Under the SDK's 30s mandatory threshold, so every request refreshes.
			fs := newFederationServer(t, 10)

			clearCredentialEnv(t)
			t.Setenv("ANTHROPIC_BASE_URL", fs.URL)
			t.Setenv("ANTHROPIC_IDENTITY_TOKEN", "jwt-from-env")
			setGitHubActionsEnv(t, oidc.requestURL())

			pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
				tt.block: nestedValue(t, tt.block, map[string]tftypes.Value{
					"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
					"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
					"github_actions":     githubActionsValue(t, nil),
				}),
			}))
			for range 2 {
				if err := tt.client(pd).Get(context.Background(), "/v1/models", nil, nil); err != nil {
					t.Fatalf("request failed: %v", err)
				}
			}
			checkOneOIDCRequestPerExchange(t, fs, oidc, 2)
		})
	}
}

// checkOneOIDCRequestPerExchange checks that each of the want exchanges
// presented a token of its own, fetched by one GET with the job's request
// token, the runner's query and the default audience.
func checkOneOIDCRequestPerExchange(t *testing.T, fs *federationServer, oidc *oidcServer, want int) {
	t.Helper()

	exchanges, _ := fs.snapshot()
	if len(exchanges) != want {
		t.Fatalf("exchanges = %d, want %d (one per request, since each token was near expiry)", len(exchanges), want)
	}
	for i, ex := range exchanges {
		if want := fmt.Sprintf("jwt-%d", i+1); ex["assertion"] != want {
			t.Errorf("exchange %d assertion = %q, want %q", i+1, ex["assertion"], want)
		}
	}
	requests := oidc.snapshot()
	if len(requests) != want {
		t.Fatalf("OIDC requests = %d, want exactly one per exchange", len(requests))
	}
	for i, r := range requests {
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer request-token" ||
			query.Get("audience") != "https://api.anthropic.com" || query.Get("api-version") != "2.0" {
			t.Errorf("OIDC request %d = %s %s (Authorization %q), want a GET with the request token, the runner's query and the default audience",
				i+1, r.Method, r.URL, r.Header.Get("Authorization"))
		}
	}
}

// TestGitHubActionsTokenURL: the runner's URL already carries a query, so
// the audience is appended to it, escaped as a query value, and the runner's
// own parameters are kept as they are.
func TestGitHubActionsTokenURL(t *testing.T) {
	for _, tt := range []struct {
		requestURL, audience, want string
	}{
		{
			requestURL: "https://example.actions.githubusercontent.com/idtoken?api-version=2.0",
			audience:   "https://api.anthropic.com",
			want:       "https://example.actions.githubusercontent.com/idtoken?api-version=2.0&audience=https%3A%2F%2Fapi.anthropic.com",
		},
		{
			requestURL: "https://example.actions.githubusercontent.com/idtoken?api-version=2.0",
			audience:   "api://my app&x=y#z",
			want:       "https://example.actions.githubusercontent.com/idtoken?api-version=2.0&audience=api%3A%2F%2Fmy+app%26x%3Dy%23z",
		},
		{
			requestURL: "https://example.actions.githubusercontent.com/idtoken",
			audience:   "https://api.anthropic.com",
			want:       "https://example.actions.githubusercontent.com/idtoken?audience=https%3A%2F%2Fapi.anthropic.com",
		},
	} {
		got, err := githubActionsTokenURL(tt.requestURL, tt.audience)
		if err != nil {
			t.Fatalf("githubActionsTokenURL(%q, %q): %v", tt.requestURL, tt.audience, err)
		}
		if got != tt.want {
			t.Errorf("githubActionsTokenURL(%q, %q) = %q, want %q", tt.requestURL, tt.audience, got, tt.want)
		}
		if parsed, _ := url.Parse(got); parsed.Query().Get("audience") != tt.audience {
			t.Errorf("audience read back from %q = %q, want %q", got, parsed.Query().Get("audience"), tt.audience)
		}
	}

	for _, requestURL := range []string{"/idtoken?api-version=2.0", "file:///etc/passwd", "https://", "%zz"} {
		if _, err := githubActionsTokenURL(requestURL, "aud"); err == nil || !strings.Contains(err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL") {
			t.Errorf("githubActionsTokenURL(%q) err = %v, want an invalid-URL error", requestURL, err)
		}
	}
}

// TestGitHubActionsIdentityTokenAudience: a configured audience is the one
// requested; unset, the rule's default audience is.
func TestGitHubActionsIdentityTokenAudience(t *testing.T) {
	for _, tt := range []struct {
		audience types.String
		want     string
	}{
		{types.StringNull(), "https://api.anthropic.com"},
		{types.StringValue("https://github.com/my-org"), "https://github.com/my-org"},
	} {
		oidc := newOIDCServer(t)
		clearCredentialEnv(t)
		setGitHubActionsEnv(t, oidc.requestURL())

		fn, err := identityTokenSource(context.Background(), federationModel{
			IdentityTokenCommand: types.ListNull(types.StringType),
			GitHubActions:        githubActionsObject(tt.audience),
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got, err := fn(context.Background()); err != nil || got != "jwt-1" {
			t.Fatalf("token = %q, %v, want jwt-1", got, err)
		}
		if got := oidc.snapshot()[0].URL.Query().Get("audience"); got != tt.want {
			t.Errorf("audience = %q, want %q", got, tt.want)
		}
	}
}

// TestConfigureGitHubActionsRequiresTheRequestVariables: GitHub sets both
// variables only in a job granted `permissions: id-token: write`, so their
// absence fails the plan with a diagnostic naming the permission and the
// missing variables, rather than the first exchange with a transport error.
func TestConfigureGitHubActionsRequiresTheRequestVariables(t *testing.T) {
	for _, tt := range []struct {
		name  string
		env   map[string]string
		unset string
	}{
		{"neither", nil, "neither is set"},
		{"no token", map[string]string{"ACTIONS_ID_TOKEN_REQUEST_URL": "https://example.com/idtoken?api-version=2.0"}, "ACTIONS_ID_TOKEN_REQUEST_TOKEN is not set"},
		{"no URL", map[string]string{"ACTIONS_ID_TOKEN_REQUEST_TOKEN": "request-token"}, "ACTIONS_ID_TOKEN_REQUEST_URL is not set"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearCredentialEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			resp := configureProvider(t, map[string]tftypes.Value{
				"federation": federationValue(t, map[string]tftypes.Value{
					"organization_id":    tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000000"),
					"federation_rule_id": tftypes.NewValue(tftypes.String, "fdrl_rule"),
					"github_actions":     githubActionsValue(t, nil),
				}),
			})
			if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Invalid Identity Token Source" {
				t.Fatalf("diagnostics = %v, want Invalid Identity Token Source", resp.Diagnostics)
			}
			detail := resp.Diagnostics.Errors()[0].Detail()
			if !strings.Contains(detail, "permissions: id-token: write") || !strings.Contains(detail, tt.unset) {
				t.Errorf("detail = %q, want the permission named and %q", detail, tt.unset)
			}
		})
	}
}

// TestGitHubActionsIdentityTokenRejectsNon200: the status is reported, the
// body only reaches the debug log, and the request is not retried.
func TestGitHubActionsIdentityTokenRejectsNon200(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, `{"message":"Can't issue ID_TOKEN, eyJbody"}`, http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := githubActionsIdentityToken(srv.URL+"?audience=x", "request-token", githubActionsIdentityTokenTimeout)(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") || strings.Contains(err.Error(), "eyJbody") || !strings.Contains(err.Error(), "TF_LOG=DEBUG") {
		t.Errorf("err = %v, want the status without the body, and a pointer to the debug log", err)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("requests = %d, want 1 (no retry)", n)
	}
}

// TestGitHubActionsIdentityTokenRejectsMalformedBody: a 200 without a token
// is an error that never quotes the body, which may hold a token in some
// other shape.
func TestGitHubActionsIdentityTokenRejectsMalformedBody(t *testing.T) {
	for _, body := range []string{`eyJnot-json`, `{}`, `{"value":""}`, `{"value":"  "}`, `{"value":42}`, `["eyJarray"]`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)

			_, err := githubActionsIdentityToken(srv.URL, "request-token", githubActionsIdentityTokenTimeout)(context.Background())
			if err == nil || !strings.Contains(err.Error(), "without a token") || strings.Contains(err.Error(), "eyJ") {
				t.Errorf("err = %v, want a missing-token error that omits the body", err)
			}
		})
	}
}

// TestGitHubActionsIdentityTokenKeepsTokensOutOfErrors: neither the request
// token nor anything GitHub answered reaches an error, which Terraform prints
// and CI logs keep.
func TestGitHubActionsIdentityTokenKeepsTokensOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, r.Header.Get("Authorization"), http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := githubActionsIdentityToken(srv.URL, "secret-request-token", githubActionsIdentityTokenTimeout)(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-request-token") {
		t.Errorf("err = %v, want a failure that omits the request token", err)
	}
}

// TestGitHubActionsIdentityTokenTimesOut: a stalled endpoint is abandoned
// after the timeout, rather than stalling the request that triggered the
// exchange.
func TestGitHubActionsIdentityTokenTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	_, err := githubActionsIdentityToken(srv.URL, "request-token", 100*time.Millisecond)(context.Background())
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("returned after %s, want the timeout", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "did not answer within 100ms") {
		t.Errorf("err = %v, want a timeout error", err)
	}
}

// TestGitHubActionsIdentityTokenReportsCancellation: a caller cancelling the
// exchange (Ctrl-C) is not a failure of the endpoint.
func TestGitHubActionsIdentityTokenReportsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := githubActionsIdentityToken(srv.URL, "request-token", githubActionsIdentityTokenTimeout)(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestIdentityTokenSourceGitHubActionsIsOneSource: github_actions counts in
// the at-most-one check, against each of the other sources.
func TestIdentityTokenSourceGitHubActionsIsOneSource(t *testing.T) {
	for name, m := range map[string]federationModel{
		"identity_token":         {IdentityToken: types.StringValue("jwt"), IdentityTokenCommand: types.ListNull(types.StringType)},
		"identity_token_file":    {IdentityTokenFile: types.StringValue("/tmp/token"), IdentityTokenCommand: types.ListNull(types.StringType)},
		"identity_token_command": {IdentityTokenCommand: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("true")})},
	} {
		t.Run(name, func(t *testing.T) {
			clearCredentialEnv(t)
			setGitHubActionsEnv(t, "https://example.com/idtoken?api-version=2.0")

			m.GitHubActions = githubActionsObject(types.StringNull())
			_, err := identityTokenSource(context.Background(), m)
			if err == nil || !strings.Contains(err.Error(), "at most one") || !strings.Contains(err.Error(), "github_actions") {
				t.Errorf("err = %v, want a conflict error naming github_actions", err)
			}
		})
	}
}

// TestGitHubActionsAudienceValidator: an empty audience fails the plan rather
// than requesting a token for an empty `aud`.
func TestGitHubActionsAudienceValidator(t *testing.T) {
	ctx := context.Background()
	schemaResp := &provider.SchemaResponse{}
	(&AnthropicProvider{version: "test"}).Schema(ctx, provider.SchemaRequest{}, schemaResp)
	for _, block := range []string{"federation", "api_federation"} {
		nested := schemaResp.Schema.Attributes[block].(schema.SingleNestedAttribute)
		audience := nested.Attributes["github_actions"].(schema.SingleNestedAttribute).Attributes["audience"].(schema.StringAttribute)

		for value, valid := range map[string]bool{"https://api.anthropic.com": true, "": false} {
			resp := &validator.StringResponse{}
			for _, v := range audience.Validators {
				v.ValidateString(ctx, validator.StringRequest{
					Path:        path.Root(block).AtName("github_actions").AtName("audience"),
					ConfigValue: types.StringValue(value),
				}, resp)
			}
			if got := !resp.Diagnostics.HasError(); got != valid {
				t.Errorf("%s.github_actions.audience %q: valid = %v, want %v", block, value, got, valid)
			}
		}
	}
}
