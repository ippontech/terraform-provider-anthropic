// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the characterization tests from the code under test")

// configureCase is one provider configuration of
// TestConfigureCharacterization.
type configureCase struct {
	name   string
	env    map[string]string
	config func(t *testing.T) map[string]tftypes.Value
}

func unknownString() tftypes.Value { return tftypes.NewValue(tftypes.String, tftypes.UnknownValue) }

// validFederation is a federation block that resolves.
func validFederation(t *testing.T) tftypes.Value {
	t.Helper()
	return federationValue(t, map[string]tftypes.Value{
		"organization_id":    tfString("00000000-0000-0000-0000-000000000000"),
		"federation_rule_id": tfString("fdrl_rule"),
		"identity_token":     tfString("jwt"),
	})
}

// configureCases walks every branch of Configure, resolveAWS and
// resolveGoogleCloud that ends in a diagnostic or a served ProviderData,
// several errors at once where they accumulate.
func configureCases() []configureCase {
	fixed := func(m map[string]tftypes.Value) func(*testing.T) map[string]tftypes.Value {
		return func(*testing.T) map[string]tftypes.Value { return m }
	}
	ws := tfString("wrkspc_01Characterization")
	keys := map[string]any{"region": awsTestRegion, "access_key": awsTestAccessKey, "secret_key": awsTestSecretKey}
	token := map[string]any{"project": gcTestProject, "access_token": gcTestToken}
	return []configureCase{
		// First party.
		{"unknown workspace_id", nil, fixed(map[string]tftypes.Value{"workspace_id": unknownString(), "api_key": tfString("k")})},
		{"no credential", nil, fixed(nil)},
		{"api_key", nil, fixed(map[string]tftypes.Value{"api_key": tfString("k"), "workspace_id": ws})},
		{"every first-party credential", nil, fixed(map[string]tftypes.Value{"api_key": tfString("k"), "admin_api_key": tfString("a"), "auth_token": tfString("t")})},
		{"credentials from the environment", map[string]string{"ANTHROPIC_API_KEY": "k", "ANTHROPIC_ADMIN_API_KEY": "a", "ANTHROPIC_AUTH_TOKEN": "t"}, fixed(nil)},
		{"federation", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"federation": validFederation(t)}
		}},
		{"federation and auth_token", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"federation": validFederation(t), "auth_token": tfString("t")}
		}},
		{"federation and unknown auth_token", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"federation": validFederation(t), "auth_token": unknownString()}
		}},
		{"federation and ANTHROPIC_AUTH_TOKEN", map[string]string{"ANTHROPIC_AUTH_TOKEN": "t"}, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"federation": validFederation(t)}
		}},
		{"federation without organization and rule", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"federation": federationValue(t, map[string]tftypes.Value{"identity_token": tfString("jwt")})}
		}},

		// Platform selection.
		{"aws and google_cloud", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "google_cloud": googleCloudValue(token)})},

		// Claude Platform on AWS.
		{"aws with every first-party credential", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys), "api_key": tfString("k"),
				"admin_api_key": tfString("a"), "auth_token": unknownString(), "federation": validFederation(t)}
		}},
		{"aws unknown block", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": tftypes.NewValue(tftypes.Object{AttributeTypes: awsAttrTypes}, tftypes.UnknownValue)})},
		{"aws unknown attributes", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{
			"region": tftypes.UnknownValue, "profile": tftypes.UnknownValue, "api_key": tftypes.UnknownValue, "access_key": tftypes.UnknownValue,
			"secret_key": tftypes.UnknownValue, "session_token": tftypes.UnknownValue, "base_url": tftypes.UnknownValue,
		})})},
		{"aws three credential sources and a lone access_key", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{
			"region": awsTestRegion, "api_key": "k", "access_key": awsTestAccessKey, "profile": "p",
		})})},
		{"aws api_key and a lone secret_key", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{
			"region": awsTestRegion, "api_key": "k", "secret_key": awsTestSecretKey,
		})})},
		{"aws lone session_token, no workspace, no region", nil, fixed(map[string]tftypes.Value{"aws": awsValue(map[string]any{"session_token": "s"})})},
		{"aws keys", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(keys)})},
		{"aws keys, region and workspace from the environment", map[string]string{"AWS_DEFAULT_REGION": awsTestRegion, awsWorkspaceIDEnv: "wrkspc_env", "ANTHROPIC_WORKSPACE_ID": "wrkspc_first_party"},
			fixed(map[string]tftypes.Value{"aws": awsValue(map[string]any{"access_key": awsTestAccessKey, "secret_key": awsTestSecretKey, "session_token": "s"})})},
		{"aws api_key and base_url without region", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"api_key": "k", "base_url": "https://gateway.invalid"})})},
		{"aws api_key without region", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{"api_key": "k"})})},
		{"aws keys and base_url without region", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(map[string]any{
			"access_key": awsTestAccessKey, "secret_key": awsTestSecretKey, "base_url": "https://gateway.invalid",
		})})},
		{"aws api_key and base_url from the environment without region", map[string]string{"ANTHROPIC_AWS_API_KEY": "k", "ANTHROPIC_AWS_BASE_URL": "https://gateway.invalid"},
			fixed(map[string]tftypes.Value{"workspace_id": ws, "aws": awsValue(nil)})},

		// Claude Models on Google Cloud.
		{"google_cloud with every first-party credential", nil, func(t *testing.T) map[string]tftypes.Value {
			t.Helper()
			return map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token), "api_key": unknownString(),
				"admin_api_key": tfString("a"), "auth_token": tfString("t"), "federation": validFederation(t)}
		}},
		{"google_cloud unknown block", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": tftypes.NewValue(tftypes.Object{AttributeTypes: gcAttrTypes}, tftypes.UnknownValue)})},
		{"google_cloud unknown attributes", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{
			"project": tftypes.UnknownValue, "location": tftypes.UnknownValue, "credentials": tftypes.UnknownValue,
			"access_token": tftypes.UnknownValue, "base_url": tftypes.UnknownValue,
		})})},
		{"google_cloud credentials and access_token", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{
			"access_token": gcTestToken, "credentials": authorizedUserJSON("http://127.0.0.1:9/token"),
		})})},
		{"google_cloud credentials not JSON", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"credentials": "not json"})})},
		{"google_cloud credentials of another type", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{"credentials": `{"type":"external_account_authorized_user_x"}`})})},
		{"google_cloud access_token", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(token)})},
		{"google_cloud access_token without workspace", nil, fixed(map[string]tftypes.Value{"google_cloud": googleCloudValue(token)})},
		{"google_cloud workspace and project from the environment", map[string]string{googleCloudWorkspaceIDEnv: "wrkspc_env", "ANTHROPIC_GOOGLE_CLOUD_PROJECT": gcTestProject, "ANTHROPIC_WORKSPACE_ID": "wrkspc_first_party"},
			fixed(map[string]tftypes.Value{"google_cloud": googleCloudValue(map[string]any{"access_token": gcTestToken})})},
		{"google_cloud authorized_user credentials", nil, fixed(map[string]tftypes.Value{"workspace_id": ws, "google_cloud": googleCloudValue(map[string]any{
			"project": gcTestProject, "credentials": authorizedUserJSON("http://127.0.0.1:9/token"),
		})})},
	}
}

// describeConfigure renders what Configure reported and served: every
// diagnostic in order with its severity, attribute path, summary and detail,
// then the platform and clients of the ProviderData, if any.
func describeConfigure(p *AnthropicProvider, resp *provider.ConfigureResponse) string {
	var b strings.Builder
	for _, d := range resp.Diagnostics {
		at := ""
		if dp, ok := d.(diag.DiagnosticWithPath); ok {
			at = " at " + dp.Path().String()
		}
		fmt.Fprintf(&b, "%s%s: %s\n    %s\n", d.Severity(), at, d.Summary(), d.Detail())
	}
	pd, ok := resp.ResourceData.(*providerdata.ProviderData)
	if !ok {
		fmt.Fprintf(&b, "served: nothing (ResourceData %T, DataSourceData %T, platform %s)\n", resp.ResourceData, resp.DataSourceData, p.configuredPlatform())
		return b.String()
	}
	fmt.Fprintf(&b, "served: platform %s, workspaceless %t, same data for data sources %t, standard %t, admin %t, oauth %t\n",
		pd.Platform, p.workspaceless.Load(), resp.DataSourceData == resp.ResourceData,
		pd.Client != nil, pd.AdminClient != nil, pd.OAuthClient != nil && pd.OAuthClient.Client != nil)
	return b.String()
}

// TestConfigureCharacterization pins what Configure reports and serves for
// every configuration branch: each diagnostic, in order, with its severity,
// attribute path, summary and exact detail, then the ProviderData. It was
// recorded before Configure and resolveAWS were split under the complexity
// cap, so the split changes none of it. Rewrite the golden file only for a
// deliberate change: go test ./internal/provider/ -run TestConfigureCharacterization -update.
func TestConfigureCharacterization(t *testing.T) {
	var got strings.Builder
	for _, c := range configureCases() {
		t.Run(c.name, func(t *testing.T) {
			isolateGoogleEnv(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			p := &AnthropicProvider{version: "test"}
			resp := configureProviderInstance(t, p, c.config(t))
			fmt.Fprintf(&got, "== %s\n%s", c.name, describeConfigure(p, resp))
		})
	}

	golden := filepath.Join("testdata", "configure_characterization.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to record it)", err)
	}
	if got.String() != string(want) {
		gotLines, wantLines := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
		for i := range max(len(gotLines), len(wantLines)) {
			var g, w string
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Fatalf("Configure behaviour changed at line %d of %s:\ngot:  %q\nwant: %q", i+1, golden, g, w)
			}
		}
	}
}
