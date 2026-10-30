// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/googlecloud"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"golang.org/x/oauth2"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
)

const (
	gcTestProject   = "my-project"
	gcTestWorkspace = "wrkspc_01GcTestWorkspace"
	gcTestToken     = "ya29.static-test-token"
)

// isolateGoogleEnv removes every variable Application Default Credentials or
// the SDK's Google Cloud client read, and points GOOGLE_APPLICATION_CREDENTIALS
// at a credential file whose token endpoint is a closed local port, so no test
// can reach gcloud's credentials, a real token endpoint or a metadata server.
func isolateGoogleEnv(t *testing.T) {
	t.Helper()
	isolateAWSEnv(t)
	for _, k := range []string{
		"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_AUTH_ACCESS_TOKEN", "GOOGLE_OAUTH_ACCESS_TOKEN",
		"GCE_METADATA_HOST", "ANTHROPIC_GOOGLE_CLOUD_PROJECT", "ANTHROPIC_GOOGLE_CLOUD_LOCATION",
		"ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID", "ANTHROPIC_GOOGLE_CLOUD_BASE_URL",
	} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADCFile(t, "http://127.0.0.1:9/token"))
}

// writeADCFile writes an authorized_user credential file whose token
// endpoint is tokenURI, and returns its path.
func writeADCFile(t *testing.T, tokenURI string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "adc.json")
	writeFile(t, f, authorizedUserJSON(tokenURI))
	return f
}

func authorizedUserJSON(tokenURI string) string {
	return fmt.Sprintf(`{"type":"authorized_user","client_id":"fake-client","client_secret":"fake-secret",`+
		`"refresh_token":"fake-refresh","token_uri":%q}`, tokenURI)
}

var gcAttrTypes = map[string]tftypes.Type{
	"project": tftypes.String, "location": tftypes.String, "credentials": tftypes.String,
	"access_token": tftypes.String, "base_url": tftypes.String,
}

func googleCloudValue(attrs map[string]any) tftypes.Value {
	values := map[string]tftypes.Value{}
	for name, typ := range gcAttrTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range attrs {
		values[name] = tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(tftypes.Object{AttributeTypes: gcAttrTypes}, values)
}

// fakeGoogleGateway records the requests it receives and answers like the API.
type fakeGoogleGateway struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newFakeGoogleGateway(t *testing.T) *fakeGoogleGateway {
	t.Helper()
	g := &fakeGoogleGateway{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.reqs = append(g.reqs, r.Clone(r.Context()))
		g.mu.Unlock()
		jsonResponse(w, 200, `{"id":"x","type":"model","data":[],"has_more":false}`)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogleGateway) requests() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*http.Request(nil), g.reqs...)
}

// fakeTokenEndpoint is a Google OAuth token endpoint that hands out token.
func fakeTokenEndpoint(t *testing.T, token string) (*httptest.Server, *atomic.Int32, *url.Values) {
	t.Helper()
	var hits atomic.Int32
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.ParseForm()
		form = r.PostForm
		jsonResponse(w, 200, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, token))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &form
}

// assertGoogleRequest: a Google access token as bearer, and neither an Anthropic
// key nor a workspace header, which this gateway must not receive.
func assertGoogleRequest(t *testing.T, r *http.Request, token string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("%s %s: authorization = %q, want the Google token", r.Method, r.URL, got)
	}
	for _, h := range []string{"X-Api-Key", "Anthropic-Workspace-Id"} {
		if v := r.Header.Get(h); v != "" {
			t.Errorf("%s %s: %s sent: %q", r.Method, r.URL, h, v)
		}
	}
}

func configureGoogleCloud(t *testing.T, block map[string]any, workspace string) *providerdata.ProviderData {
	t.Helper()
	config := map[string]tftypes.Value{"google_cloud": googleCloudValue(block)}
	if workspace != "" {
		config["workspace_id"] = tfString(workspace)
	}
	return providerDataFrom(t, configureProvider(t, config))
}

// TestConfigureGoogleCloudStaticToken: access_token becomes the bearer of
// every request, standard and Admin API alike; the first-party variables,
// ANTHROPIC_WORKSPACE_ID included, have no effect, and the configured
// workspace never becomes a header.
func TestConfigureGoogleCloudStaticToken(t *testing.T) {
	isolateGoogleEnv(t)
	exportFirstPartyEnvironment(t)
	gw := newFakeGoogleGateway(t)

	pd := configureGoogleCloud(t, map[string]any{"project": gcTestProject, "access_token": gcTestToken, "base_url": gw.srv.URL}, gcTestWorkspace)
	if pd.Platform != platform.GoogleCloud || pd.OAuthClient != nil || pd.AdminClient == nil {
		t.Fatalf("ProviderData = %+v", pd)
	}

	ctx := context.Background()
	if _, err := pd.Client.Models.Get(ctx, "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pd.AdminClient.DoRequest(ctx, "GET", "/v1/organizations/external_keys", nil); err != nil {
		t.Fatal(err)
	}
	reqs := gw.requests()
	if len(reqs) != 2 {
		t.Fatalf("gateway received %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		assertGoogleRequest(t, r, gcTestToken)
	}
}

// TestConfigureGoogleCloudApplicationDefaultCredentials: with no credential
// in the block, ADC reads GOOGLE_APPLICATION_CREDENTIALS, an offline file
// whose token endpoint is a fake; the token is fetched once and reused.
func TestConfigureGoogleCloudApplicationDefaultCredentials(t *testing.T) {
	isolateGoogleEnv(t)
	exportFirstPartyEnvironment(t)
	gw := newFakeGoogleGateway(t)
	tokens, hits, form := fakeTokenEndpoint(t, "ya29.from-adc")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADCFile(t, tokens.URL+"/token"))

	pd := configureGoogleCloud(t, map[string]any{"project": gcTestProject, "base_url": gw.srv.URL}, gcTestWorkspace)
	for range 2 {
		if _, err := pd.Client.Models.Get(context.Background(), "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range gw.requests() {
		assertGoogleRequest(t, r, "ya29.from-adc")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (cached)", n)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "fake-refresh" {
		t.Errorf("token request form = %v", *form)
	}
}

// TestConfigureGoogleCloudCredentials: credentials wins over ADC and, for a
// service account key, gives the project when none is configured.
func TestConfigureGoogleCloudCredentials(t *testing.T) {
	t.Run("authorized_user", func(t *testing.T) {
		isolateGoogleEnv(t)
		gw := newFakeGoogleGateway(t)
		adcTokens, adcHits, _ := fakeTokenEndpoint(t, "ya29.adc-must-not-be-used")
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADCFile(t, adcTokens.URL+"/token"))
		tokens, _, _ := fakeTokenEndpoint(t, "ya29.from-credentials")

		pd := configureGoogleCloud(t, map[string]any{
			"project": gcTestProject, "credentials": authorizedUserJSON(tokens.URL + "/token"), "base_url": gw.srv.URL,
		}, gcTestWorkspace)
		if _, err := pd.Client.Models.Get(context.Background(), "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
			t.Fatal(err)
		}
		assertGoogleRequest(t, gw.requests()[0], "ya29.from-credentials")
		if adcHits.Load() != 0 {
			t.Error("ADC was used although credentials is set")
		}
	})

	t.Run("service_account gives the project", func(t *testing.T) {
		isolateGoogleEnv(t)
		tokens, _, form := fakeTokenEndpoint(t, "ya29.from-service-account")
		capture := &capturingTransport{}

		// Configure through the model directly: the derived URL is what is
		// checked, so the client is built with a capturing transport.
		block := map[string]attr.Value{}
		for name := range gcAttrTypes {
			block[name] = types.StringNull()
		}
		block["credentials"] = types.StringValue(serviceAccountJSON(t, "sa-project", tokens.URL+"/token"))
		attrTypes := map[string]attr.Type{}
		for name := range gcAttrTypes {
			attrTypes[name] = types.StringType
		}
		settings, diags := resolveGoogleCloud(context.Background(), AnthropicProviderModel{
			WorkspaceID: types.StringValue(gcTestWorkspace),
			GoogleCloud: types.ObjectValueMust(attrTypes, block),
		})
		if diags.HasError() {
			t.Fatal(diags)
		}
		if settings.config.Project != "sa-project" {
			t.Fatalf("project = %q, want the service account's", settings.config.Project)
		}
		client, err := newGoogleCloudClient(context.Background(), settings.config, option.WithHTTPClient(&http.Client{Transport: capture}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Models.Get(context.Background(), "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
			t.Fatal(err)
		}
		if want := "https://claude.googleapis.com/v1/projects/sa-project/locations/global/workspaces/" + gcTestWorkspace + "/invoke/v1/models/claude-opus-5-5"; capture.urls[0] != want {
			t.Errorf("URL = %q, want %q", capture.urls[0], want)
		}
		if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || form.Get("assertion") == "" {
			t.Errorf("token request form = %v", *form)
		}
	})
}

// serviceAccountJSON is a service account key file with a freshly generated
// key, whose token endpoint is tokenURI.
func serviceAccountJSON(t *testing.T, project, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	b, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": project, "private_key_id": "k1", "private_key": string(pemKey),
		"client_email": "terraform@" + project + ".iam.gserviceaccount.com", "client_id": "1", "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestNewGoogleCloudClientDerivesTheURLs: without base_url, the SDK sends
// each request to the workspace endpoint, or to the organization endpoint for
// /v1/organizations routes, whatever ANTHROPIC_BASE_URL says. The URLs are
// captured before any network access.
func TestNewGoogleCloudClientDerivesTheURLs(t *testing.T) {
	tests := []struct {
		name, location, workspace string
		wantModel, wantOrg        string
	}{
		{
			name: "default location", workspace: gcTestWorkspace,
			wantModel: "https://claude.googleapis.com/v1/projects/my-project/locations/global/workspaces/" + gcTestWorkspace + "/invoke/v1/models/claude-opus-5-5",
			wantOrg:   "https://claude.googleapis.com/v1/projects/my-project/locations/global/claudeOrganization/invoke/v1/organizations/external_keys?limit=20",
		},
		{
			name: "explicit location", location: "us-east5", workspace: gcTestWorkspace,
			wantModel: "https://claude.googleapis.com/v1/projects/my-project/locations/us-east5/workspaces/" + gcTestWorkspace + "/invoke/v1/models/claude-opus-5-5",
			wantOrg:   "https://claude.googleapis.com/v1/projects/my-project/locations/us-east5/claudeOrganization/invoke/v1/organizations/external_keys?limit=20",
		},
		{
			name:    "no workspace: organization routes only",
			wantOrg: "https://claude.googleapis.com/v1/projects/my-project/locations/global/claudeOrganization/invoke/v1/organizations/external_keys?limit=20",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateGoogleEnv(t)
			t.Setenv("ANTHROPIC_BASE_URL", "https://first-party.example.invalid")
			capture := &capturingTransport{}
			client, err := newGoogleCloudClient(context.Background(), googlecloud.ClientConfig{
				Project: gcTestProject, Location: tt.location, WorkspaceID: tt.workspace,
				TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: gcTestToken}),
			}, option.WithHTTPClient(&http.Client{Transport: capture}), option.WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			_, modelErr := client.Models.Get(ctx, "claude-opus-5-5", anthropic.ModelGetParams{})
			if _, err := admin.NewSDKClient(client).DoRequest(ctx, "GET", "/v1/organizations/external_keys?limit=20", nil); err != nil {
				t.Fatal(err)
			}

			want := []string{tt.wantOrg}
			if tt.workspace != "" {
				if modelErr != nil {
					t.Fatal(modelErr)
				}
				want = []string{tt.wantModel, tt.wantOrg}
			} else if modelErr == nil || !strings.Contains(modelErr.Error(), "no workspace ID") {
				t.Errorf("a workspace route without a workspace: err = %v, want the SDK's refusal before sending", modelErr)
			}
			if strings.Join(capture.urls, "\n") != strings.Join(want, "\n") {
				t.Errorf("URLs =\n%s\nwant\n%s", strings.Join(capture.urls, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// TestConfigureGoogleCloudRejects covers the configuration errors of the
// google_cloud block.
func TestConfigureGoogleCloudRejects(t *testing.T) {
	token := map[string]any{"project": gcTestProject, "access_token": gcTestToken}
	ws := tfString(gcTestWorkspace)
	tests := []struct {
		name        string
		config      map[string]tftypes.Value
		wantSummary string
		wantDetail  string
	}{
		{"aws", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "aws": awsValue(map[string]any{"region": awsTestRegion})}, "Conflicting Platform Configuration", "aws and google_cloud"},
		{"api_key", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "api_key": tfString("k")}, "Conflicting Platform Configuration", "google_cloud and api_key"},
		{"admin_api_key", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "admin_api_key": tfString("k")}, "Conflicting Platform Configuration", "google_cloud and admin_api_key"},
		{"auth_token", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "auth_token": tfString("t")}, "Conflicting Platform Configuration", "google_cloud and auth_token"},
		{"federation", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "federation": federationValue(t, nil)}, "Conflicting Platform Configuration", "google_cloud and federation"},
		{"unknown block", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": tftypes.NewValue(tftypes.Object{AttributeTypes: gcAttrTypes}, tftypes.UnknownValue)}, "Unknown Google Cloud Configuration", "google_cloud block"},
		{"unknown access_token", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"project": gcTestProject, "access_token": tftypes.UnknownValue})}, "Unknown Google Cloud Configuration", "google_cloud.access_token"},
		{"unknown project", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"project": tftypes.UnknownValue, "access_token": gcTestToken})}, "Unknown Google Cloud Configuration", "google_cloud.project"},
		{"credentials and access_token", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"access_token": gcTestToken, "credentials": authorizedUserJSON("http://127.0.0.1:9/token")})}, "Conflicting Google Cloud Credentials", "at most one"},
		{"credentials not json", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"credentials": "not json"})}, "Invalid Google Cloud Credentials", "not a JSON credential file"},
		{"oauth client configuration", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"credentials": `{"installed":{"client_id":"x"}}`})}, "Invalid Google Cloud Credentials", "accepted types"},
		{"no project", map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"access_token": gcTestToken})}, "Unable to Configure Claude Models on Google Cloud", "no project found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateGoogleEnv(t)
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

// TestConfigureGoogleCloudEnvironment: the platform's variables fill an empty
// block (project, workspace, base URL), and alone select nothing.
func TestConfigureGoogleCloudEnvironment(t *testing.T) {
	isolateGoogleEnv(t)
	gw := newFakeGoogleGateway(t)
	tokens, _, _ := fakeTokenEndpoint(t, "ya29.from-adc")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADCFile(t, tokens.URL+"/token"))
	t.Setenv("ANTHROPIC_GOOGLE_CLOUD_PROJECT", gcTestProject)
	t.Setenv("ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID", gcTestWorkspace)
	t.Setenv("ANTHROPIC_GOOGLE_CLOUD_BASE_URL", gw.srv.URL)
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_first_party")

	resp := configureProvider(t, nil)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Missing Credentials" {
		t.Fatalf("without the block: diagnostics = %v, want the first-party Missing Credentials error", resp.Diagnostics)
	}

	p := &AnthropicProvider{version: "test"}
	pd := providerDataFrom(t, configureProviderInstance(t, p, map[string]tftypes.Value{"google_cloud": googleCloudValue(nil)}))
	if p.workspaceless.Load() {
		t.Error("ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID was not read")
	}
	if _, err := pd.Client.Models.Get(context.Background(), "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
		t.Fatal(err)
	}
	assertGoogleRequest(t, gw.requests()[0], "ya29.from-adc")
}

// TestGoogleCloudGate: workspaces are unavailable on Google Cloud; without a
// workspace, a type that calls workspace routes is refused at Configure,
// while one limited to organization routes is served.
func TestGoogleCloudGate(t *testing.T) {
	isolateGoogleEnv(t)
	ctx := context.Background()
	for _, workspace := range []string{gcTestWorkspace, ""} {
		p := &AnthropicProvider{version: "test"}
		config := map[string]tftypes.Value{"google_cloud": googleCloudValue(map[string]any{"project": gcTestProject, "access_token": gcTestToken})}
		if workspace != "" {
			config["workspace_id"] = tfString(workspace)
		}
		providerDataFrom(t, configureProviderInstance(t, p, config))

		byName := map[string]resource.Resource{}
		for _, f := range p.Resources(ctx) {
			r := f()
			byName[resourceTypeName(r)] = r
		}
		configureErr := func(name string) string {
			var resp resource.ConfigureResponse
			byName[name].(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: &providerdata.ProviderData{}}, &resp)
			if !resp.Diagnostics.HasError() {
				return ""
			}
			return resp.Diagnostics.Errors()[0].Summary()
		}

		if got := configureErr("anthropic_workspace"); got != "anthropic_workspace Not Available on Claude Models on Google Cloud (Anthropic managed)" {
			t.Errorf("workspace=%q: anthropic_workspace refusal = %q", workspace, got)
		}
		_, agentRefused := byName["anthropic_agent"].(*refusedResource)
		if workspace != "" && agentRefused {
			t.Error("anthropic_agent refused although a workspace is set")
		}
		if workspace == "" {
			if got := configureErr("anthropic_agent"); got != "anthropic_agent Requires a Workspace on Claude Models on Google Cloud (Anthropic managed)" {
				t.Errorf("no workspace: anthropic_agent refusal = %q", got)
			}
			orgOnly := registration[resource.Resource]{New: agents.NewAgentResource, GoogleCloud: platform.Supported().OnOrganizationRoutes()}
			if _, refused := p.gateResource(orgOnly)().(*refusedResource); refused {
				t.Error("no workspace: a type limited to organization routes was refused")
			}
		}
	}
}

// TestRegistryDeclaresGoogleCloudForEveryType: see the AWS counterpart.
func TestRegistryDeclaresGoogleCloudForEveryType(t *testing.T) {
	for _, reg := range resourceRegistrations() {
		if reg.GoogleCloud.State == platform.StateUndeclared {
			t.Errorf("resource %s declares no Google Cloud availability", resourceTypeName(reg.New()))
		}
	}
	for _, reg := range dataSourceRegistrations() {
		if reg.GoogleCloud.State == platform.StateUndeclared {
			t.Errorf("data source %s declares no Google Cloud availability", dataSourceTypeName(reg.New()))
		}
	}
}
