// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// secrets is the write-only part of a configuration: token, access_token,
// secret_value, and the refresh block's refresh_token and client_secret (the
// refresh block is written only when one of these two is set). nil is null.
type secrets struct {
	token, accessToken, secretValue, refreshToken, clientSecret any
}

func (s secrets) config(t *testing.T) tfsdk.Config {
	t.Helper()
	r := NewVaultCredentialResource()
	refreshType := schematest.ResourceObjectType(t, r).AttributeTypes["refresh"].(tftypes.Object)
	teaType := refreshType.AttributeTypes["token_endpoint_auth"].(tftypes.Object)
	refresh := tftypes.NewValue(refreshType, nil)
	if s.refreshToken != nil || s.clientSecret != nil {
		refresh = tftypes.NewValue(refreshType, map[string]tftypes.Value{
			"client_id":      tftypes.NewValue(tftypes.String, "client-1"),
			"refresh_token":  tftypes.NewValue(tftypes.String, s.refreshToken),
			"token_endpoint": tftypes.NewValue(tftypes.String, "https://auth.example.com/token"),
			"resource":       tftypes.NewValue(tftypes.String, nil),
			"scope":          tftypes.NewValue(tftypes.String, nil),
			"token_endpoint_auth": tftypes.NewValue(teaType, map[string]tftypes.Value{
				"type":          tftypes.NewValue(tftypes.String, "client_secret_basic"),
				"client_secret": tftypes.NewValue(tftypes.String, s.clientSecret),
			}),
		})
	}
	return schematest.Config(t, r, map[string]any{
		"token": s.token, "access_token": s.accessToken, "secret_value": s.secretValue, "refresh": refresh,
	})
}

// credentialPlan is a planned credential of type typ with the given
// non-secret auth attributes; the others are null.
func credentialPlan(typ string, set func(*VaultCredentialResourceModel)) *VaultCredentialResourceModel {
	m := &VaultCredentialResourceModel{
		Type:              types.StringValue(typ),
		MCPServerURL:      types.StringNull(),
		ExpiresAt:         types.StringNull(),
		Refresh:           types.ObjectNull(credentialRefreshAttrTypes),
		SecretName:        types.StringNull(),
		Networking:        types.ObjectNull(credentialNetworkingAttrTypes),
		InjectionLocation: types.ObjectNull(credentialInjectionLocationAttrTypes),
	}
	if set != nil {
		set(m)
	}
	return m
}

// refreshObject is a planned refresh block (secrets are null in a plan).
func refreshObject(resource, scope types.String, tea types.Object) types.Object {
	return types.ObjectValueMust(credentialRefreshAttrTypes, map[string]attr.Value{
		"client_id": types.StringValue("client-1"), "refresh_token": types.StringNull(),
		"token_endpoint": types.StringValue("https://auth.example.com/token"),
		"resource":       resource, "scope": scope, "token_endpoint_auth": tea,
	})
}

func teaObject(typ string) types.Object {
	return types.ObjectValueMust(credentialTokenEndpointAuthAttrTypes, map[string]attr.Value{
		"type": types.StringValue(typ), "client_secret": types.StringNull(),
	})
}

func networkingObject(mode string, hosts types.List) types.Object {
	return types.ObjectValueMust(credentialNetworkingAttrTypes, map[string]attr.Value{"mode": types.StringValue(mode), "allowed_hosts": hosts})
}

func hostList(hosts ...string) types.List {
	values := make([]attr.Value, len(hosts))
	for i, h := range hosts {
		values[i] = types.StringValue(h)
	}
	return types.ListValueMust(types.StringType, values)
}

type authCase struct {
	plan    *VaultCredentialResourceModel
	secrets secrets
	want    string // the auth JSON, or the error summary when err
	err     bool
}

func runAuthCases[T any](t *testing.T, cases map[string]authCase, build func(context.Context, tfsdk.Config, *VaultCredentialResourceModel) (T, diag.Diagnostics)) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			auth, diags := build(context.Background(), tc.secrets.config(t), tc.plan)
			if tc.err {
				if !diags.HasError() || diags.Errors()[0].Summary() != tc.want {
					t.Errorf("diagnostics = %v, want %q", diags, tc.want)
				}
				if got := authJSON(t, auth); got != "null" {
					t.Errorf("auth on error = %s, want the zero value", got)
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			if diff := sdktest.JSONDiff(authJSON(t, auth), tc.want); diff != "" {
				t.Errorf("auth %s differs from %s:\n%s", authJSON(t, auth), tc.want, diff)
			}
		})
	}
}

const (
	oauthURL = "https://mcp.example.com/sse"
	expiry   = "2027-01-01T00:00:00Z"
)

// TestBuildCreateAuthUnion pins the create auth of every type: the secrets
// from the configuration, the planned attributes, null and unknown optional
// ones left out.
func TestBuildCreateAuthUnion(t *testing.T) {
	t.Parallel()
	oauth := func(set func(*VaultCredentialResourceModel)) *VaultCredentialResourceModel {
		return credentialPlan("mcp_oauth", func(m *VaultCredentialResourceModel) {
			m.MCPServerURL = types.StringValue(oauthURL)
			if set != nil {
				set(m)
			}
		})
	}
	env := func(networking, location types.Object) *VaultCredentialResourceModel {
		return credentialPlan("environment_variable", func(m *VaultCredentialResourceModel) {
			m.SecretName, m.Networking, m.InjectionLocation = types.StringValue("API_KEY"), networking, location
		})
	}
	runAuthCases(t, map[string]authCase{
		"static_bearer": {
			plan:    credentialPlan("static_bearer", func(m *VaultCredentialResourceModel) { m.MCPServerURL = types.StringValue("https://mcp.example.com") }),
			secrets: secrets{token: "t1"},
			want:    `{"type":"static_bearer","mcp_server_url":"https://mcp.example.com","token":"t1"}`,
		},
		"mcp_oauth without expiry or refresh": {
			plan:    oauth(func(m *VaultCredentialResourceModel) { m.ExpiresAt = types.StringUnknown() }),
			secrets: secrets{accessToken: "a1"},
			want:    `{"type":"mcp_oauth","mcp_server_url":"` + oauthURL + `","access_token":"a1"}`,
		},
		"mcp_oauth with client_secret_basic": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.ExpiresAt = types.StringValue(expiry)
				m.Refresh = refreshObject(types.StringValue("https://mcp.example.com"), types.StringValue("read"), teaObject("client_secret_basic"))
			}),
			secrets: secrets{accessToken: "a1", refreshToken: "r1", clientSecret: "c1"},
			want: `{"type":"mcp_oauth","mcp_server_url":"` + oauthURL + `","access_token":"a1","expires_at":"` + expiry + `",` +
				`"refresh":{"client_id":"client-1","refresh_token":"r1","token_endpoint":"https://auth.example.com/token",` +
				`"resource":"https://mcp.example.com","scope":"read","token_endpoint_auth":{"type":"client_secret_basic","client_secret":"c1"}}}`,
		},
		"mcp_oauth with none": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringUnknown(), teaObject("none"))
			}),
			secrets: secrets{accessToken: "a1", refreshToken: "r1"},
			want: `{"type":"mcp_oauth","mcp_server_url":"` + oauthURL + `","access_token":"a1",` +
				`"refresh":{"client_id":"client-1","refresh_token":"r1","token_endpoint":"https://auth.example.com/token","token_endpoint_auth":{"type":"none"}}}`,
		},
		"mcp_oauth with client_secret_post": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringNull(), teaObject("client_secret_post"))
			}),
			secrets: secrets{accessToken: "a1", refreshToken: "r1", clientSecret: "c1"},
			want: `{"type":"mcp_oauth","mcp_server_url":"` + oauthURL + `","access_token":"a1",` +
				`"refresh":{"client_id":"client-1","refresh_token":"r1","token_endpoint":"https://auth.example.com/token","token_endpoint_auth":{"type":"client_secret_post","client_secret":"c1"}}}`,
		},
		"environment_variable limited": {
			plan:    env(networkingObject("limited", hostList("a.example.com")), injectionLocationObj(types.BoolValue(true), types.BoolValue(false))),
			secrets: secrets{secretValue: "v1"},
			want: `{"type":"environment_variable","secret_name":"API_KEY","secret_value":"v1",` +
				`"networking":{"type":"limited","allowed_hosts":["a.example.com"]},"injection_location":{"header":true,"body":false}}`,
		},
		"environment_variable unrestricted, location unknown": {
			plan:    env(networkingObject("unrestricted", types.ListNull(types.StringType)), types.ObjectUnknown(credentialInjectionLocationAttrTypes)),
			secrets: secrets{secretValue: "v1"},
			want:    `{"type":"environment_variable","secret_name":"API_KEY","secret_value":"v1","networking":{"type":"unrestricted"}}`,
		},
		"environment_variable location with an unknown field": {
			plan:    env(networkingObject("unrestricted", types.ListNull(types.StringType)), injectionLocationObj(types.BoolValue(true), types.BoolUnknown())),
			secrets: secrets{secretValue: "v1"},
			want:    `{"type":"environment_variable","secret_name":"API_KEY","secret_value":"v1","networking":{"type":"unrestricted"}}`,
		},
		"invalid expires_at": {
			plan:    oauth(func(m *VaultCredentialResourceModel) { m.ExpiresAt = types.StringValue("tomorrow") }),
			secrets: secrets{accessToken: "a1"},
			want:    "Invalid timestamp", err: true,
		},
		"unknown type": {plan: credentialPlan("other", nil), want: "Internal Error", err: true},
	}, buildCreateAuthUnion)
}

// TestBuildUpdateAuthUnion pins the update auth of every type: an empty or
// null secret is left out (the API keeps the stored one), the refresh block
// sends its secrets, scope and the client_secret_* method, never none.
func TestBuildUpdateAuthUnion(t *testing.T) {
	t.Parallel()
	oauth := func(set func(*VaultCredentialResourceModel)) *VaultCredentialResourceModel {
		return credentialPlan("mcp_oauth", set)
	}
	env := func(networking, location types.Object) *VaultCredentialResourceModel {
		return credentialPlan("environment_variable", func(m *VaultCredentialResourceModel) {
			m.SecretName, m.Networking, m.InjectionLocation = types.StringValue("API_KEY"), networking, location
		})
	}
	runAuthCases(t, map[string]authCase{
		"static_bearer rotated":     {plan: credentialPlan("static_bearer", nil), secrets: secrets{token: "t2"}, want: `{"type":"static_bearer","token":"t2"}`},
		"static_bearer empty token": {plan: credentialPlan("static_bearer", nil), secrets: secrets{token: ""}, want: `{"type":"static_bearer"}`},
		"static_bearer null token":  {plan: credentialPlan("static_bearer", nil), want: `{"type":"static_bearer"}`},
		"mcp_oauth nothing to send": {plan: oauth(nil), secrets: secrets{accessToken: ""}, want: `{"type":"mcp_oauth"}`},
		"mcp_oauth with client_secret_basic": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.ExpiresAt = types.StringValue(expiry)
				m.Refresh = refreshObject(types.StringValue("https://mcp.example.com"), types.StringValue("read"), teaObject("client_secret_basic"))
			}),
			secrets: secrets{accessToken: "a2", refreshToken: "r2", clientSecret: "c2"},
			want: `{"type":"mcp_oauth","access_token":"a2","expires_at":"` + expiry + `",` +
				`"refresh":{"refresh_token":"r2","scope":"read","token_endpoint_auth":{"type":"client_secret_basic","client_secret":"c2"}}}`,
		},
		"mcp_oauth with client_secret_basic, empty secrets": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringNull(), teaObject("client_secret_basic"))
			}),
			secrets: secrets{accessToken: "a2", refreshToken: "", clientSecret: ""},
			want:    `{"type":"mcp_oauth","access_token":"a2","refresh":{"token_endpoint_auth":{"type":"client_secret_basic"}}}`,
		},
		"mcp_oauth with client_secret_post": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringUnknown(), teaObject("client_secret_post"))
			}),
			secrets: secrets{refreshToken: "r2", clientSecret: "c2"},
			want:    `{"type":"mcp_oauth","refresh":{"refresh_token":"r2","token_endpoint_auth":{"type":"client_secret_post","client_secret":"c2"}}}`,
		},
		"mcp_oauth with client_secret_post, empty secret": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringNull(), teaObject("client_secret_post"))
			}),
			secrets: secrets{refreshToken: "r2", clientSecret: ""},
			want:    `{"type":"mcp_oauth","refresh":{"refresh_token":"r2","token_endpoint_auth":{"type":"client_secret_post"}}}`,
		},
		"mcp_oauth with none": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringNull(), teaObject("none"))
			}),
			secrets: secrets{refreshToken: "r2"},
			want:    `{"type":"mcp_oauth","refresh":{"refresh_token":"r2"}}`,
		},
		"mcp_oauth refresh without token_endpoint_auth": {
			plan: oauth(func(m *VaultCredentialResourceModel) {
				m.Refresh = refreshObject(types.StringNull(), types.StringValue("read"), types.ObjectNull(credentialTokenEndpointAuthAttrTypes))
			}),
			secrets: secrets{refreshToken: "r2"},
			want:    `{"type":"mcp_oauth","refresh":{"refresh_token":"r2","scope":"read"}}`,
		},
		"environment_variable full": {
			plan:    env(networkingObject("limited", hostList("a.example.com", "b.example.com")), injectionLocationObj(types.BoolValue(false), types.BoolValue(true))),
			secrets: secrets{secretValue: "v2"},
			want: `{"type":"environment_variable","secret_value":"v2",` +
				`"networking":{"type":"limited","allowed_hosts":["a.example.com","b.example.com"]},"injection_location":{"header":false,"body":true}}`,
		},
		"environment_variable nothing to send": {
			plan:    env(types.ObjectUnknown(credentialNetworkingAttrTypes), types.ObjectNull(credentialInjectionLocationAttrTypes)),
			secrets: secrets{secretValue: ""},
			want:    `{"type":"environment_variable"}`,
		},
		"invalid expires_at": {
			plan: oauth(func(m *VaultCredentialResourceModel) { m.ExpiresAt = types.StringValue("tomorrow") }),
			want: "Invalid timestamp", err: true,
		},
		"unknown type": {plan: credentialPlan("other", nil), want: "Internal Error", err: true},
	}, buildUpdateAuthUnion)
}
