// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const baseURLTestPassword = "s3cr3t-value"

// withUserInfo puts user:baseURLTestPassword@ in front of the host of srvURL.
func withUserInfo(srvURL string) string {
	return strings.Replace(srvURL, "http://", "http://proxy-user:"+baseURLTestPassword+"@", 1)
}

var wantBasic = "Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:"+baseURLTestPassword))

// failingAnswers answers like gatewayAnswers, except a 500 API error on
// /v1/models/broken, whose *anthropic.Error text starts with the request URL.
func failingAnswers(w http.ResponseWriter, r *http.Request, n int) {
	if strings.HasSuffix(r.URL.Path, "/v1/models/broken") {
		jsonResponse(w, 500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
		return
	}
	gatewayAnswers(w, r, n)
}

// TestAWSBaseURLUserInfoBecomesAHeader: user information in the base URL,
// configured or from ANTHROPIC_AWS_BASE_URL, reaches the server as the Basic
// Authorization header net/http sent for it with an API key, a SigV4
// signature still replaces it, and no error repeats the password.
func TestAWSBaseURLUserInfoBecomesAHeader(t *testing.T) {
	for _, tt := range []struct {
		name      string
		block     map[string]any
		fromEnv   bool
		wantBasic bool
	}{
		{"api_key, attribute", map[string]any{"region": awsTestRegion, "api_key": "aws-console-key"}, false, true},
		{"api_key, variable", map[string]any{"region": awsTestRegion, "api_key": "aws-console-key"}, true, true},
		{"SigV4, attribute", map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateAWSEnv(t)
			gw := newSigV4Gateway(t, awssdk.Credentials{AccessKeyID: awsTestAccessKey, SecretAccessKey: awsTestSecretKey}, failingAnswers)
			if tt.fromEnv {
				t.Setenv(awsBaseURLEnv, withUserInfo(gw.srv.URL))
			} else {
				tt.block["base_url"] = withUserInfo(gw.srv.URL)
			}
			resp := configureProvider(t, map[string]tftypes.Value{"workspace_id": tfString(awsTestWorkspace), "aws": awsValue(tt.block)})
			assertNoPassword(t, resp.Diagnostics.Errors())
			pd := providerDataFrom(t, resp)

			ctx := context.Background()
			if _, err := pd.Client.Models.Get(ctx, "claude-sonnet-5-5", anthropic.ModelGetParams{}); err != nil {
				t.Fatalf("request: %v", err)
			}
			_, err := pd.Client.Models.Get(ctx, "broken", anthropic.ModelGetParams{})
			if err == nil {
				t.Fatal("the failing route succeeded")
			}
			if strings.Contains(err.Error(), baseURLTestPassword) {
				t.Errorf("the SDK error repeats the password: %v", err)
			}

			r := gw.recorded()[0]
			got := r.header.Get("Authorization")
			if tt.wantBasic {
				if got != wantBasic || r.header.Get("X-Api-Key") != "aws-console-key" {
					t.Errorf("authorization = %q, x-api-key = %q, want the Basic credentials and the key", got, r.header.Get("X-Api-Key"))
				}
			} else if r.sigErr != nil {
				t.Errorf("signature: %v", r.sigErr)
			}
		})
	}
}

// TestGoogleCloudBaseURLUserInfoIsDropped: on Google Cloud the token stays
// the Authorization header, as net/http left the user information out of a
// request that carried it.
func TestGoogleCloudBaseURLUserInfoIsDropped(t *testing.T) {
	isolateGoogleEnv(t)
	gw := newFakeGoogleGateway(t)

	pd := configureGoogleCloud(t, map[string]any{"project": gcTestProject, "access_token": gcTestToken, "base_url": withUserInfo(gw.srv.URL)}, gcTestWorkspace)
	if _, err := pd.Client.Models.Get(context.Background(), "claude-opus-5-5", anthropic.ModelGetParams{}); err != nil {
		t.Fatal(err)
	}
	reqs := gw.requests()
	if len(reqs) != 1 {
		t.Fatalf("gateway received %d requests, want 1", len(reqs))
	}
	assertGoogleRequest(t, reqs[0], gcTestToken)
}

// TestConfigurePlatformRejectsAnUnparsableBaseURL: the error names the
// attribute or the variable and repeats neither the value nor the parse
// error, which quotes it.
func TestConfigurePlatformRejectsAnUnparsableBaseURL(t *testing.T) {
	notAURL := "https://user:" + baseURLTestPassword + "@gateway.invalid:port"
	awsKeys := awsValue(map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey, "base_url": notAURL})
	for _, tt := range []struct {
		name       string
		config     map[string]tftypes.Value
		env        map[string]string
		attr       path.Path
		wantDetail string
	}{
		{"aws attribute", map[string]tftypes.Value{"workspace_id": tfString(awsTestWorkspace), "aws": awsKeys}, nil,
			path.Root("aws").AtName("base_url"), "aws.base_url is not a valid URL"},
		{"google_cloud variable", map[string]tftypes.Value{"workspace_id": tfString(gcTestWorkspace), "google_cloud": googleCloudValue(map[string]any{"project": gcTestProject, "access_token": gcTestToken})},
			map[string]string{googleCloudBaseURLEnv: notAURL},
			path.Root("google_cloud").AtName("base_url"), "The ANTHROPIC_GOOGLE_CLOUD_BASE_URL environment variable is not a valid URL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateGoogleEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			resp := configureProviderInstance(t, &AnthropicProvider{version: "test"}, tt.config)
			if !resp.Diagnostics.HasError() {
				t.Fatal("Configure succeeded")
			}
			e := resp.Diagnostics.Errors()[0]
			if e.Summary() != "Invalid Base URL" || !strings.Contains(e.Detail(), tt.wantDetail) {
				t.Errorf("error = %q: %q, want %q containing %q", e.Summary(), e.Detail(), "Invalid Base URL", tt.wantDetail)
			}
			if wp, ok := e.(interface{ Path() path.Path }); !ok || !wp.Path().Equal(tt.attr) {
				t.Errorf("error is not on %s", tt.attr)
			}
			assertNoPassword(t, resp.Diagnostics)
		})
	}
}

func assertNoPassword[D interface {
	Summary() string
	Detail() string
}](t *testing.T, diags []D) {
	t.Helper()
	for _, d := range diags {
		if strings.Contains(d.Summary()+d.Detail(), baseURLTestPassword) {
			t.Errorf("a diagnostic repeats the password: %q: %q", d.Summary(), d.Detail())
		}
	}
}

// TestGatewayBaseURL: the URL keeps everything but its user information,
// and a URL without any is passed through with no header.
func TestGatewayBaseURL(t *testing.T) {
	isolateGoogleEnv(t)
	got, opts := gatewayBaseURL("https://u:p@gateway.example.com/v1/projects/p", awsBaseURLEnv, true)
	if got != "https://gateway.example.com/v1/projects/p" || len(opts) != 1 {
		t.Errorf("got %q with %d options", got, len(opts))
	}
	if got, opts := gatewayBaseURL("https://u:p@gateway.example.com", googleCloudBaseURLEnv, false); got != "https://gateway.example.com" || opts != nil {
		t.Errorf("without basic: got %q with %d options", got, len(opts))
	}
	for _, v := range []string{"", "https://vpce-0123.aws-external-anthropic.eu-west-1.vpce.amazonaws.com"} {
		if got, opts := gatewayBaseURL(v, awsBaseURLEnv, true); got != v || opts != nil {
			t.Errorf("%q: got %q with %d options", v, got, len(opts))
		}
	}
}
