// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// tunnelType is what the configure tests need from each resource and data
// source of the package, behind one shape.
type tunnelType struct {
	name      string
	typeName  string
	metadata  func() string
	configure func(providerData any) diag.Diagnostics
	client    func() *providerdata.OAuthClient
}

func tunnelTypes() []tunnelType {
	ctx := context.Background()
	tunnel := NewTunnelResource().(*TunnelResource)
	certificate := NewTunnelCertificateResource().(*TunnelCertificateResource)
	tunnelDS := NewTunnelDataSource().(*TunnelDataSource)
	tunnelsDS := NewTunnelsDataSource().(*TunnelsDataSource)

	forResource := func(name, typeName string, r resource.ResourceWithConfigure, client func() *providerdata.OAuthClient) tunnelType {
		return tunnelType{
			name: name, typeName: typeName, client: client,
			metadata: func() string {
				var resp resource.MetadataResponse
				r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "anthropic"}, &resp)
				return resp.TypeName
			},
			configure: func(providerData any) diag.Diagnostics {
				var resp resource.ConfigureResponse
				r.Configure(ctx, resource.ConfigureRequest{ProviderData: providerData}, &resp)
				return resp.Diagnostics
			},
		}
	}
	forDataSource := func(name, typeName string, d datasource.DataSourceWithConfigure, client func() *providerdata.OAuthClient) tunnelType {
		return tunnelType{
			name: name, typeName: typeName, client: client,
			metadata: func() string {
				var resp datasource.MetadataResponse
				d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "anthropic"}, &resp)
				return resp.TypeName
			},
			configure: func(providerData any) diag.Diagnostics {
				var resp datasource.ConfigureResponse
				d.Configure(ctx, datasource.ConfigureRequest{ProviderData: providerData}, &resp)
				return resp.Diagnostics
			},
		}
	}
	return []tunnelType{
		forResource("tunnel resource", "anthropic_tunnel", tunnel, func() *providerdata.OAuthClient { return tunnel.client }),
		forResource("certificate resource", "anthropic_tunnel_certificate", certificate, func() *providerdata.OAuthClient { return certificate.client }),
		forDataSource("tunnel data source", "anthropic_tunnel", tunnelDS, func() *providerdata.OAuthClient { return tunnelDS.client }),
		forDataSource("tunnels data source", "anthropic_tunnels", tunnelsDS, func() *providerdata.OAuthClient { return tunnelsDS.client }),
	}
}

func TestTunnelTypes_Metadata(t *testing.T) {
	t.Parallel()
	for _, tt := range tunnelTypes() {
		if got := tt.metadata(); got != tt.typeName {
			t.Errorf("%s: type name = %q, want %q", tt.name, got, tt.typeName)
		}
	}
}

// Every tunnel type takes the OAuth client and refuses a provider without
// one: the tunnels API accepts only a workspace:manage_tunnels bearer token.
func TestTunnelTypes_Configure(t *testing.T) {
	t.Parallel()
	srv, _ := fakeTunnelsAPI(t, nil)
	oauth := oauthtest.NewClient(t, srv)

	tests := []struct {
		name         string
		providerData any
		wantErr      string
		wantClient   bool
	}{
		{"no provider data yet", nil, "", false},
		{"unexpected provider data", "not provider data", "Unexpected", false},
		{"no OAuth client", &providerdata.ProviderData{}, "Missing OAuth Token", false},
		{"OAuth wrapper without an SDK client", &providerdata.ProviderData{OAuthClient: &providerdata.OAuthClient{}}, "Missing OAuth Token", false},
		{"OAuth client", &providerdata.ProviderData{OAuthClient: oauth}, "", true},
	}
	for _, tc := range tests {
		for _, tt := range tunnelTypes() {
			diags := tt.configure(tc.providerData)
			switch {
			case tc.wantErr == "" && diags.HasError():
				t.Errorf("%s, %s: unexpected diagnostics %v", tt.name, tc.name, diags)
			case tc.wantErr != "" && (!diags.HasError() || !containsSummary(diags, tc.wantErr)):
				t.Errorf("%s, %s: diagnostics = %v, want an error starting with %q", tt.name, tc.name, diags, tc.wantErr)
			}
			if got := tt.client(); (got == oauth) != tc.wantClient {
				t.Errorf("%s, %s: client = %p, want the OAuth client: %v", tt.name, tc.name, got, tc.wantClient)
			}
		}
	}
}

func containsSummary(diags diag.Diagnostics, prefix string) bool {
	for _, d := range diags.Errors() {
		if strings.HasPrefix(d.Summary(), prefix) {
			return true
		}
	}
	return false
}

// TestTunnelResource_ImportState: the tunnel ID is imported as is; Read then
// fills the rest, and reveals the token once (TestTunnelResource_Read_importRevealsOnce).
func TestTunnelResource_ImportState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &TunnelResource{}

	resp := &resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "tnl_01ABC"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState returned errors: %v", resp.Diagnostics)
	}
	var id types.String
	if diags := resp.State.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		t.Fatalf("id: %v", diags)
	}
	if id.ValueString() != "tnl_01ABC" {
		t.Errorf("id = %v, want tnl_01ABC", id)
	}
}

// The plan modifier and the validator describe themselves in plans and docs.
func TestTunnelSchemaHelpers_descriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, d := range map[string]interface {
		Description(context.Context) string
		MarkdownDescription(context.Context) string
	}{
		"tokenFollowsRotation":      tokenFollowsRotation(),
		"caCertificatePEMValidator": caCertificatePEMValidator{},
	} {
		if d.Description(ctx) == "" || d.MarkdownDescription(ctx) != d.Description(ctx) {
			t.Errorf("%s: description = %q, markdown = %q", name, d.Description(ctx), d.MarkdownDescription(ctx))
		}
	}
}
