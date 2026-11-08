// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const baseURLTestPassword = "s3cr3t-value"

// TestConfigurePlatformRejectsBaseURLCredentials: a gateway base URL with
// user information fails Configure, configured or read from the SDK's
// variable, and the error never repeats the password.
func TestConfigurePlatformRejectsBaseURLCredentials(t *testing.T) {
	withUser := "https://user:" + baseURLTestPassword + "@gateway.invalid"
	notAURL := "https://user:" + baseURLTestPassword + "@gateway.invalid:port"
	awsKeys := func(extra map[string]any) tftypes.Value {
		m := map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey}
		for k, v := range extra {
			m[k] = v
		}
		return awsValue(m)
	}
	gcToken := func(extra map[string]any) tftypes.Value {
		m := map[string]any{"project": gcTestProject, "access_token": gcTestToken}
		for k, v := range extra {
			m[k] = v
		}
		return googleCloudValue(m)
	}
	tests := []struct {
		name       string
		config     map[string]tftypes.Value
		env        map[string]string
		attr       path.Path
		wantDetail string
	}{
		{"aws attribute", map[string]tftypes.Value{"workspace_id": tfString(awsTestWorkspace), "aws": awsKeys(map[string]any{"base_url": withUser})}, nil,
			path.Root("aws").AtName("base_url"), "aws.base_url carries user information"},
		{"aws variable", map[string]tftypes.Value{"workspace_id": tfString(awsTestWorkspace), "aws": awsKeys(nil)}, map[string]string{awsBaseURLEnv: withUser},
			path.Root("aws").AtName("base_url"), "The ANTHROPIC_AWS_BASE_URL environment variable carries user information"},
		{"aws not a URL", map[string]tftypes.Value{"workspace_id": tfString(awsTestWorkspace), "aws": awsKeys(map[string]any{"base_url": notAURL})}, nil,
			path.Root("aws").AtName("base_url"), "aws.base_url is not a valid URL"},
		{"google_cloud attribute", map[string]tftypes.Value{"workspace_id": tfString(gcTestWorkspace), "google_cloud": gcToken(map[string]any{"base_url": withUser})}, nil,
			path.Root("google_cloud").AtName("base_url"), "google_cloud.base_url carries user information"},
		{"google_cloud variable", map[string]tftypes.Value{"workspace_id": tfString(gcTestWorkspace), "google_cloud": gcToken(nil)}, map[string]string{googleCloudBaseURLEnv: withUser},
			path.Root("google_cloud").AtName("base_url"), "The ANTHROPIC_GOOGLE_CLOUD_BASE_URL environment variable carries user information"},
		{"google_cloud not a URL", map[string]tftypes.Value{"workspace_id": tfString(gcTestWorkspace), "google_cloud": gcToken(nil)}, map[string]string{googleCloudBaseURLEnv: notAURL},
			path.Root("google_cloud").AtName("base_url"), "The ANTHROPIC_GOOGLE_CLOUD_BASE_URL environment variable is not a valid URL"},
	}
	for _, tt := range tests {
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
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Summary()+d.Detail(), baseURLTestPassword) {
					t.Errorf("a diagnostic repeats the password: %q: %q", d.Summary(), d.Detail())
				}
			}
		})
	}
}

// TestCheckGatewayBaseURLAcceptsPlainURLs: a URL without user information,
// with or without a path, and an unset value pass.
func TestCheckGatewayBaseURLAcceptsPlainURLs(t *testing.T) {
	isolateGoogleEnv(t)
	for _, v := range []string{"", "https://vpce-0123.aws-external-anthropic.eu-west-1.vpce.amazonaws.com", "https://gateway.example.com/v1/projects/p/locations/l"} {
		if d := checkGatewayBaseURL(path.Root("aws").AtName("base_url"), "aws.base_url", v, awsBaseURLEnv); d.HasError() {
			t.Errorf("%q: %v", v, d)
		}
	}
}
