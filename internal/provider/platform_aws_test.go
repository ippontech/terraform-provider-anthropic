// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicaws "github.com/anthropics/anthropic-sdk-go/aws"
	"github.com/anthropics/anthropic-sdk-go/option"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/workspaces"
)

const (
	awsService       = "aws-external-anthropic"
	awsTestRegion    = "us-west-2"
	awsTestWorkspace = "wrkspc_01AwsTestWorkspace"
	// AWS's documented example credentials: never valid anywhere.
	awsTestAccessKey = "AKIAIOSFODNN7EXAMPLE"
	awsTestSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

// isolateAWSEnv removes every variable the AWS SDK's credential chain or the
// Anthropic SDK's AWS client reads, and points the shared configuration files
// at empty ones, so a test sees no ambient AWS identity: no ~/.aws, no exported
// key, no instance metadata service.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	clearCredentialEnv(t)
	for _, k := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN",
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_CA_BUNDLE",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"ANTHROPIC_AWS_API_KEY", "ANTHROPIC_AWS_WORKSPACE_ID", "ANTHROPIC_AWS_BASE_URL",
	} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
	dir := t.TempDir()
	for k, name := range map[string]string{"AWS_CONFIG_FILE": "config", "AWS_SHARED_CREDENTIALS_FILE": "credentials"} {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k, f)
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

var awsAttrTypes = map[string]tftypes.Type{
	"region": tftypes.String, "profile": tftypes.String, "api_key": tftypes.String,
	"access_key": tftypes.String, "secret_key": tftypes.String, "session_token": tftypes.String,
	"base_url": tftypes.String,
}

// awsValue is an aws block holding attrs, every other attribute null.
func awsValue(attrs map[string]any) tftypes.Value {
	values := map[string]tftypes.Value{}
	for name, typ := range awsAttrTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range attrs {
		values[name] = tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(tftypes.Object{AttributeTypes: awsAttrTypes}, values)
}

func tfString(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }

// recordedRequest is one request the fake gateway received.
type recordedRequest struct {
	method, path, rawPath string
	header                http.Header
	body                  []byte
	// sigErr is nil when the SigV4 signature recomputed from the request
	// matches the one it carries.
	sigErr error
}

// sigv4Gateway is a fake Claude Platform on AWS gateway. It recomputes each
// request's SigV4 signature with the AWS SDK's own signer, the expected
// credentials and the X-Amz-Date the request carries, over exactly the headers
// the request says it signed, and records whether the two match. respond
// answers the n-th request (from 1).
type sigv4Gateway struct {
	srv     *httptest.Server
	creds   awssdk.Credentials
	region  string
	respond func(w http.ResponseWriter, r *http.Request, n int)

	mu       sync.Mutex
	requests []recordedRequest
}

func newSigV4Gateway(t *testing.T, creds awssdk.Credentials, respond func(http.ResponseWriter, *http.Request, int)) *sigv4Gateway {
	t.Helper()
	g := &sigv4Gateway{creds: creds, region: awsTestRegion, respond: respond}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.requests = append(g.requests, recordedRequest{
			method: r.Method, path: r.URL.Path, rawPath: r.URL.EscapedPath(),
			header: r.Header.Clone(), body: body, sigErr: verifySigV4(r, body, g.creds, g.region),
		})
		n := len(g.requests)
		g.mu.Unlock()
		g.respond(w, r, n)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *sigv4Gateway) recorded() []recordedRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recordedRequest(nil), g.requests...)
}

var sigV4Header = regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=([^,]+), SignedHeaders=([^,]+), Signature=([0-9a-f]{64})$`)

func verifySigV4(r *http.Request, body []byte, creds awssdk.Credentials, region string) error {
	auth := r.Header.Get("Authorization")
	m := sigV4Header.FindStringSubmatch(auth)
	if m == nil {
		return fmt.Errorf("authorization %q is not a SigV4 signature", auth)
	}
	signingTime, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("x-amz-date: %w", err)
	}

	// The request as the client signed it: same method, host, escaped path,
	// query, signed headers and body length.
	replay, err := http.NewRequestWithContext(r.Context(), r.Method, "http://"+r.Host+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	for name := range strings.SplitSeq(m[2], ";") {
		if name != "host" {
			replay.Header[http.CanonicalHeaderKey(name)] = r.Header.Values(name)
		}
	}
	sum := sha256.Sum256(body)
	if err := v4.NewSigner().SignHTTP(r.Context(), creds, replay, hex.EncodeToString(sum[:]), awsService, region, signingTime); err != nil {
		return err
	}
	if got := replay.Header.Get("Authorization"); got != auth {
		return fmt.Errorf("signature mismatch:\n got  %s\n want %s", auth, got)
	}
	return nil
}

func jsonResponse(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// gatewayAnswers answers like the API for the routes the tests call, with a
// 429 on the first attempt of a route whose path ends in "/rate-limited".
func gatewayAnswers(w http.ResponseWriter, r *http.Request, _ int) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/v1/files"):
		jsonResponse(w, 200, `{"id":"file_1","type":"file","filename":"a.txt","mime_type":"text/plain","size_bytes":5,"created_at":"2026-10-25T00:00:00Z"}`)
	case strings.HasSuffix(r.URL.Path, "/count_tokens"):
		jsonResponse(w, 200, `{"input_tokens":3}`)
	default:
		jsonResponse(w, 200, `{"id":"x","type":"model"}`)
	}
}

// trapServer fails the test if anything reaches it: it stands for
// ANTHROPIC_BASE_URL, which a platform alias must not read.
func trapServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request sent to ANTHROPIC_BASE_URL: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// exportFirstPartyEnvironment exports every first-party variable a platform
// alias must ignore.
func exportFirstPartyEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-first-party")
	t.Setenv("ANTHROPIC_ADMIN_API_KEY", "sk-ant-admin01-first-party")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-first-party")
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_first_party")
	t.Setenv("ANTHROPIC_BASE_URL", trapServer(t).URL)
}

// TestConfigureAWSSignsEveryRequest is the SigV4 test of the aws block, with
// static credentials, with and without a session token. Every first-party
// variable is exported and must have no effect. It covers a JSON GET and
// POST, a multipart upload, an Admin API path holding an encoded KMS ARN, and
// a request replayed after a 429 (signed again, with its new retry count).
// Limit: it proves the signature is the AWS SDK signer's; whether the real
// gateway canonicalizes the requests the same way is not verified.
func TestConfigureAWSSignsEveryRequest(t *testing.T) {
	for _, sessionToken := range []string{"", "fake-session-token"} {
		t.Run(fmt.Sprintf("session token %t", sessionToken != ""), func(t *testing.T) {
			isolateAWSEnv(t)
			exportFirstPartyEnvironment(t)

			creds := awssdk.Credentials{AccessKeyID: awsTestAccessKey, SecretAccessKey: awsTestSecretKey, SessionToken: sessionToken}
			attempts := map[string]int{}
			var mu sync.Mutex
			gw := newSigV4Gateway(t, creds, func(w http.ResponseWriter, r *http.Request, n int) {
				mu.Lock()
				attempts[r.URL.Path]++
				first := attempts[r.URL.Path] == 1
				mu.Unlock()
				if first && strings.Contains(r.URL.Path, "workspaces") || first && strings.HasSuffix(r.URL.Path, "/count_tokens") {
					w.Header().Set("retry-after-ms", "1")
					jsonResponse(w, http.StatusTooManyRequests, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
					return
				}
				gatewayAnswers(w, r, n)
			})

			block := map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey, "base_url": gw.srv.URL}
			if sessionToken != "" {
				block["session_token"] = sessionToken
			}
			p := &AnthropicProvider{version: "test"}
			pd := providerDataFrom(t, configureProviderInstance(t, p, map[string]tftypes.Value{
				"workspace_id": tfString(awsTestWorkspace),
				"aws":          awsValue(block),
			}))
			if pd.Platform != platform.AWS || pd.OAuthClient != nil || pd.AdminClient == nil || p.configuredPlatform() != platform.AWS {
				t.Fatalf("ProviderData = %+v, configured platform %q", pd, p.configuredPlatform())
			}

			ctx := context.Background()
			if _, err := pd.Client.Models.Get(ctx, "claude-sonnet-5-5", anthropic.ModelGetParams{}); err != nil {
				t.Fatalf("GET model: %v", err)
			}
			if _, err := pd.Client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
				Model:    "claude-sonnet-5-5",
				Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Hello"))},
			}); err != nil {
				t.Fatalf("POST count_tokens: %v", err)
			}
			if _, err := pd.Client.Files.Upload(ctx, anthropic.FileUploadParams{
				File: anthropic.File(strings.NewReader("hello"), "a.txt", "text/plain"),
			}); err != nil {
				t.Fatalf("multipart upload: %v", err)
			}
			arn := "arn:aws:kms:us-west-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
			if _, err := pd.AdminClient.DoRequest(ctx, "GET", "/v1/organizations/external_keys/"+url.PathEscape(arn), nil); err != nil {
				t.Fatalf("GET external key: %v", err)
			}
			if _, err := pd.AdminClient.DoRequest(ctx, "POST", "/v1/organizations/workspaces", map[string]string{"name": "w"}); err != nil {
				t.Fatalf("POST workspace: %v", err)
			}

			reqs := gw.recorded()
			if len(reqs) != 7 {
				t.Fatalf("gateway received %d requests, want 7 (count_tokens and the workspace POST replayed once after a 429)", len(reqs))
			}
			retryCounts := map[string][]string{}
			for _, r := range reqs {
				label := r.method + " " + r.path
				if r.sigErr != nil {
					t.Errorf("%s: %v", label, r.sigErr)
				}
				scope := fmt.Sprintf("Credential=%s/%s/%s/%s/aws4_request", awsTestAccessKey, r.header.Get("X-Amz-Date")[:8], awsTestRegion, awsService)
				if !strings.Contains(r.header.Get("Authorization"), scope) {
					t.Errorf("%s: authorization %q lacks %q", label, r.header.Get("Authorization"), scope)
				}
				if got := r.header.Get("X-Amz-Security-Token"); got != sessionToken {
					t.Errorf("%s: x-amz-security-token = %q, want %q", label, got, sessionToken)
				}
				if v := r.header.Get("X-Api-Key"); v != "" {
					t.Errorf("%s: x-api-key sent: %q", label, v)
				}
				wantWorkspace := awsTestWorkspace
				if strings.HasPrefix(r.path, "/v1/organizations/") {
					wantWorkspace = ""
				}
				if got := r.header.Get("Anthropic-Workspace-Id"); got != wantWorkspace {
					t.Errorf("%s: anthropic-workspace-id = %q, want %q", label, got, wantWorkspace)
				}
				retryCounts[label] = append(retryCounts[label], r.header.Get("X-Stainless-Retry-Count"))
			}

			if got := retryCounts["POST /v1/messages/count_tokens"]; strings.Join(got, ",") != "0,1" {
				t.Errorf("count_tokens retry counts = %v, want [0 1]: the replay must be signed anew", got)
			}
			if got := retryCounts["POST /v1/organizations/workspaces"]; len(got) != 2 {
				t.Errorf("workspace POST sent %d times, want 2 (replayed on 429)", len(got))
			}
			upload := reqs[3]
			if !strings.HasPrefix(upload.header.Get("Content-Type"), "multipart/form-data") || !bytes.Contains(upload.body, []byte("hello")) {
				t.Errorf("upload: content-type %q, body %q", upload.header.Get("Content-Type"), upload.body)
			}
			if want := "/v1/organizations/external_keys/" + url.PathEscape(arn); reqs[4].rawPath != want {
				t.Errorf("external key path = %q, want %q", reqs[4].rawPath, want)
			}
		})
	}
}

// TestConfigureAWSCredentialSources: the other ways to authenticate. An AWS
// Console key goes out as x-api-key (SDK v1.84; the current platform page
// documents Authorization: Bearer, not verified against the gateway), never
// signed. A named profile and the default chain read credentials from
// temporary files and variables only.
func TestConfigureAWSCredentialSources(t *testing.T) {
	tests := []struct {
		name    string
		block   map[string]any
		setup   func(t *testing.T)
		wantKey string // x-api-key; empty means a SigV4 signature by profileCreds
	}{
		{
			name:    "api_key",
			block:   map[string]any{"region": awsTestRegion, "api_key": "aws-console-key"},
			wantKey: "aws-console-key",
		},
		{
			name:  "ANTHROPIC_AWS_API_KEY",
			block: map[string]any{"region": awsTestRegion},
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("ANTHROPIC_AWS_API_KEY", "aws-console-key-env")
			},
			wantKey: "aws-console-key-env",
		},
		{
			name:  "profile",
			block: map[string]any{"region": awsTestRegion, "profile": "platform-admin"},
			setup: func(t *testing.T) {
				t.Helper()
				writeFile(t, os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), fmt.Sprintf(
					"[default]\naws_access_key_id = AKIADEFAULTWRONG\naws_secret_access_key = wrong\n\n"+
						"[platform-admin]\naws_access_key_id = %s\naws_secret_access_key = %s\n", awsTestAccessKey, awsTestSecretKey))
			},
		},
		{
			name:  "default chain",
			block: map[string]any{},
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("AWS_REGION", awsTestRegion)
				t.Setenv("AWS_ACCESS_KEY_ID", awsTestAccessKey)
				t.Setenv("AWS_SECRET_ACCESS_KEY", awsTestSecretKey)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateAWSEnv(t)
			exportFirstPartyEnvironment(t)
			if tt.setup != nil {
				tt.setup(t)
			}
			gw := newSigV4Gateway(t, awssdk.Credentials{AccessKeyID: awsTestAccessKey, SecretAccessKey: awsTestSecretKey}, gatewayAnswers)
			tt.block["base_url"] = gw.srv.URL

			pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
				"workspace_id": tfString(awsTestWorkspace),
				"aws":          awsValue(tt.block),
			}))
			if _, err := pd.Client.Models.Get(context.Background(), "claude-sonnet-5-5", anthropic.ModelGetParams{}); err != nil {
				t.Fatalf("request: %v", err)
			}

			r := gw.recorded()[0]
			if got := r.header.Get("X-Api-Key"); got != tt.wantKey {
				t.Errorf("x-api-key = %q, want %q", got, tt.wantKey)
			}
			if tt.wantKey != "" {
				if r.header.Get("Authorization") != "" || r.header.Get("X-Amz-Date") != "" {
					t.Errorf("a key request was also signed: %q", r.header.Get("Authorization"))
				}
			} else if r.sigErr != nil {
				t.Errorf("signature: %v", r.sigErr)
			}
			if got := r.header.Get("Anthropic-Workspace-Id"); got != awsTestWorkspace {
				t.Errorf("anthropic-workspace-id = %q", got)
			}
		})
	}
}

func writeFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestConfigureAWSEnvironmentFillsTheBlock: with an empty block, the
// workspace, region and base URL come from the platform's own variables,
// never from ANTHROPIC_WORKSPACE_ID or ANTHROPIC_BASE_URL.
func TestConfigureAWSEnvironmentFillsTheBlock(t *testing.T) {
	isolateAWSEnv(t)
	exportFirstPartyEnvironment(t)
	gw := newSigV4Gateway(t, awssdk.Credentials{AccessKeyID: awsTestAccessKey, SecretAccessKey: awsTestSecretKey}, gatewayAnswers)
	t.Setenv("AWS_REGION", awsTestRegion)
	t.Setenv("AWS_ACCESS_KEY_ID", awsTestAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", awsTestSecretKey)
	t.Setenv("ANTHROPIC_AWS_WORKSPACE_ID", "wrkspc_from_aws_env")
	t.Setenv("ANTHROPIC_AWS_BASE_URL", gw.srv.URL)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{"aws": awsValue(nil)}))
	if _, err := pd.Client.Models.Get(context.Background(), "claude-sonnet-5-5", anthropic.ModelGetParams{}); err != nil {
		t.Fatalf("request: %v", err)
	}
	r := gw.recorded()[0]
	if r.sigErr != nil {
		t.Error(r.sigErr)
	}
	if got := r.header.Get("Anthropic-Workspace-Id"); got != "wrkspc_from_aws_env" {
		t.Errorf("anthropic-workspace-id = %q, want the ANTHROPIC_AWS_WORKSPACE_ID value", got)
	}
}

// TestConfigureAWSEnvironmentAloneDoesNothing: the platform variables fill the
// block but never select the platform.
func TestConfigureAWSEnvironmentAloneDoesNothing(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_REGION", awsTestRegion)
	t.Setenv("AWS_ACCESS_KEY_ID", awsTestAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", awsTestSecretKey)
	t.Setenv("ANTHROPIC_AWS_WORKSPACE_ID", awsTestWorkspace)
	t.Setenv("ANTHROPIC_AWS_API_KEY", "aws-console-key")

	resp := configureProvider(t, nil)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Missing Credentials" {
		t.Fatalf("diagnostics = %v, want the first-party Missing Credentials error", resp.Diagnostics)
	}

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		jsonResponse(w, 200, `{}`)
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-first-party")
	pd := providerDataFrom(t, configureProvider(t, nil))
	if pd.Platform != platform.FirstParty {
		t.Fatalf("platform = %q, want first party", pd.Platform)
	}
	if err := pd.Client.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-Api-Key") != "sk-ant-api03-first-party" || got.Get("X-Amz-Date") != "" {
		t.Errorf("first-party request headers: x-api-key %q, x-amz-date %q", got.Get("X-Api-Key"), got.Get("X-Amz-Date"))
	}
}

// TestConfigureAWSRejects covers every configuration error of the aws block,
// each reported before any client is built.
func TestConfigureAWSRejects(t *testing.T) {
	keys := map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		maps.Copy(m, keys)
		maps.Copy(m, extra)
		return m
	}
	ws := tfString(awsTestWorkspace)
	tests := []struct {
		name        string
		config      map[string]tftypes.Value
		env         map[string]string
		wantSummary string
		wantDetail  string
	}{
		{"api_key", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "api_key": tfString("sk-ant-api03-x")}, nil, "Conflicting Platform Configuration", "aws and api_key"},
		{"admin_api_key", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "admin_api_key": tfString("sk-ant-admin01-x")}, nil, "Conflicting Platform Configuration", "aws and admin_api_key"},
		{"auth_token", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "auth_token": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)}, nil, "Conflicting Platform Configuration", "aws and auth_token"},
		{"federation", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "federation": federationValue(t, nil)}, nil, "Conflicting Platform Configuration", "aws and federation"},
		{"unknown block", map[string]tftypes.Value{"workspace_id": ws, "aws": tftypes.NewValue(tftypes.Object{AttributeTypes: awsAttrTypes}, tftypes.UnknownValue)}, nil, "Unknown AWS Configuration", "aws block"},
		{"unknown region", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(with(map[string]any{"region": tftypes.UnknownValue}))}, nil, "Unknown AWS Configuration", "aws.region"},
		{"unknown secret", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(with(map[string]any{"secret_key": tftypes.UnknownValue}))}, nil, "Unknown AWS Configuration", "aws.secret_key"},
		{"unknown workspace", map[string]tftypes.Value{"workspace_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), "aws": awsValue(keys)}, nil, "Unknown Workspace Configuration", "workspace_id"},
		{"no workspace", map[string]tftypes.Value{"aws": awsValue(keys)}, map[string]string{"ANTHROPIC_WORKSPACE_ID": "wrkspc_first_party"}, "Missing Workspace", "ANTHROPIC_AWS_WORKSPACE_ID"},
		{"no region", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"access_key": awsTestAccessKey, "secret_key": awsTestSecretKey})}, nil, "Missing AWS Region", "aws.region"},
		{"api_key and access_key", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(with(map[string]any{"api_key": "k"}))}, nil, "Conflicting AWS Credentials", "at most one"},
		{"profile and access_key", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(with(map[string]any{"profile": "p"}))}, nil, "Conflicting AWS Credentials", "at most one"},
		{"access_key alone", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey})}, nil, "Incomplete AWS Credentials", "together"},
		{"session_token alone", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"region": awsTestRegion, "session_token": "t"})}, nil, "Incomplete AWS Credentials", "session_token"},
		{"unknown profile", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"region": awsTestRegion, "profile": "absent"})}, nil, "Unable to Configure Claude Platform on AWS", "absent"},
		{"no credentials anywhere", map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"region": awsTestRegion})}, nil, "Unable to Configure Claude Platform on AWS", "credentials"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateAWSEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			p := &AnthropicProvider{version: "test"}
			resp := configureProviderInstance(t, p, tt.config)
			if !resp.Diagnostics.HasError() {
				t.Fatal("Configure succeeded")
			}
			e := resp.Diagnostics.Errors()[0]
			if e.Summary() != tt.wantSummary || !strings.Contains(e.Detail(), tt.wantDetail) {
				t.Errorf("error = %q: %q, want %q containing %q", e.Summary(), e.Detail(), tt.wantSummary, tt.wantDetail)
			}
			if resp.ResourceData != nil || p.configuredPlatform() != platform.FirstParty {
				t.Error("a failed Configure still served a ProviderData or recorded a platform")
			}
		})
	}
}

// capturingTransport records each request URL as the client built it, then
// answers itself: nothing leaves the process, whatever the host.
type capturingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (c *capturingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.urls = append(c.urls, r.URL.String())
	c.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"x","data":[],"has_more":false}`)), Request: r,
	}, nil
}

// TestNewAWSClientDerivesTheGatewayURL: without base_url, every route,
// including the Admin API, goes to the regional gateway, whatever
// ANTHROPIC_BASE_URL says. The URL is captured before any network access.
func TestNewAWSClientDerivesTheGatewayURL(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://first-party.example.invalid")

	capture := &capturingTransport{}
	client, err := newAWSClient(context.Background(), anthropicaws.ClientConfig{
		AWSRegion: "eu-west-3", AWSAccessKey: awsTestAccessKey, AWSSecretAccessKey: awsTestSecretKey, WorkspaceID: awsTestWorkspace,
	}, option.WithHTTPClient(&http.Client{Transport: capture}))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := client.Models.Get(ctx, "claude-sonnet-5-5", anthropic.ModelGetParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.NewSDKClient(client).DoRequest(ctx, "GET", "/v1/organizations/workspaces?limit=100", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://aws-external-anthropic.eu-west-3.api.aws/v1/models/claude-sonnet-5-5",
		"https://aws-external-anthropic.eu-west-3.api.aws/v1/organizations/workspaces?limit=100",
	}
	if strings.Join(capture.urls, "\n") != strings.Join(want, "\n") {
		t.Errorf("URLs = %v, want %v", capture.urls, want)
	}
}

// TestConfigureAWSGatesTheRegistry: after an aws Configure, a type the
// gateway does not serve is refused at its own Configure, while supported and
// unverified types are the real ones.
func TestConfigureAWSGatesTheRegistry(t *testing.T) {
	isolateAWSEnv(t)
	p := &AnthropicProvider{version: "test"}
	providerDataFrom(t, configureProviderInstance(t, p, map[string]tftypes.Value{
		"workspace_id": tfString(awsTestWorkspace),
		"aws":          awsValue(map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey}),
	}))

	byName := map[string]string{}
	for _, f := range p.Resources(context.Background()) {
		r := f()
		byName[resourceTypeName(r)] = fmt.Sprintf("%T", r)
	}
	if got := byName["anthropic_invite"]; got != "*provider.refusedResource" {
		t.Errorf("anthropic_invite is %s, want the unavailable stand-in", got)
	}
	for name, want := range map[string]any{
		"anthropic_workspace":  workspaces.NewWorkspaceResource(),
		"anthropic_deployment": nil, // unverified: the real type
	} {
		if strings.Contains(byName[name], "refused") {
			t.Errorf("%s is refused on AWS", name)
		}
		if want != nil && byName[name] != fmt.Sprintf("%T", want) {
			t.Errorf("%s is %s", name, byName[name])
		}
	}
}

// TestRegistryDeclaresAWSForEveryType: a type added without saying whether
// Claude Platform on AWS serves it fails here, not at a user's plan.
func TestRegistryDeclaresAWSForEveryType(t *testing.T) {
	for _, reg := range resourceRegistrations() {
		if reg.AWS.State == platform.StateUndeclared {
			t.Errorf("resource %s declares no AWS availability", resourceTypeName(reg.New()))
		}
	}
	for _, reg := range dataSourceRegistrations() {
		if reg.AWS.State == platform.StateUndeclared {
			t.Errorf("data source %s declares no AWS availability", dataSourceTypeName(reg.New()))
		}
	}
}
