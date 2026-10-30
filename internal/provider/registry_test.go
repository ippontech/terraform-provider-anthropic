// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizations"
)

func resourceTypeName(r resource.Resource) string {
	var resp resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
	return resp.TypeName
}

func dataSourceTypeName(d datasource.DataSource) string {
	var resp datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
	return resp.TypeName
}

// TestRegistrationsNameEveryTypeOnce: each registration is one type, and the
// provider serves exactly the registered ones.
func TestRegistrationsNameEveryTypeOnce(t *testing.T) {
	ctx := context.Background()
	p := &AnthropicProvider{version: "test"}

	seen := map[string]bool{}
	for _, f := range p.Resources(ctx) {
		name := resourceTypeName(f())
		if !strings.HasPrefix(name, providerTypeName+"_") || seen[name] {
			t.Errorf("resource type %q is malformed or registered twice", name)
		}
		seen[name] = true
	}
	if len(seen) != len(resourceRegistrations()) {
		t.Errorf("Resources() serves %d types, the registry holds %d", len(seen), len(resourceRegistrations()))
	}

	seen = map[string]bool{}
	for _, f := range p.DataSources(ctx) {
		name := dataSourceTypeName(f())
		if !strings.HasPrefix(name, providerTypeName+"_") || seen[name] {
			t.Errorf("data source type %q is malformed or registered twice", name)
		}
		seen[name] = true
	}
	if len(seen) != len(dataSourceRegistrations()) {
		t.Errorf("DataSources() serves %d types, the registry holds %d", len(seen), len(dataSourceRegistrations()))
	}
}

// TestGateIsTransparentFirstParty is the "no change in first-party mode"
// guarantee: before Configure, and after a first-party Configure, every factory
// returns exactly the type it registered, never a stand-in.
func TestGateIsTransparentFirstParty(t *testing.T) {
	clearCredentialEnv(t)
	ctx := context.Background()

	for _, configured := range []bool{false, true} {
		p := &AnthropicProvider{version: "test"}
		if configured {
			pd := providerDataFrom(t, configureProviderInstance(t, p, map[string]tftypes.Value{
				"api_key": tftypes.NewValue(tftypes.String, "sk-ant-api03-x"),
			}))
			if pd.Platform != platform.FirstParty {
				t.Fatalf("pd.Platform = %q, want %q", pd.Platform, platform.FirstParty)
			}
		}
		if got := p.configuredPlatform(); got != platform.FirstParty {
			t.Fatalf("configured=%v: configuredPlatform() = %q, want first party", configured, got)
		}

		for i, f := range p.Resources(ctx) {
			got, want := reflect.TypeOf(f()), reflect.TypeOf(resourceRegistrations()[i].New())
			if got != want {
				t.Errorf("configured=%v: resource factory %d returned %v, want %v", configured, i, got, want)
			}
		}
		for i, f := range p.DataSources(ctx) {
			got, want := reflect.TypeOf(f()), reflect.TypeOf(dataSourceRegistrations()[i].New())
			if got != want {
				t.Errorf("configured=%v: data source factory %d returned %v, want %v", configured, i, got, want)
			}
		}
	}
}

// TestFirstPartyClientReadsEvery404AsTheAPIs: the 404 guard is a gateway
// concern. The first-party client keeps returning every 404 as the API error
// it is, whatever the body.
func TestFirstPartyClientReadsEvery404AsTheAPIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env")

	pd := providerDataFrom(t, configureProvider(t, nil))

	_, err := pd.Client.Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want an *anthropic.Error with status 404", err)
	}
	var unserved *platform.UnservedRouteError
	if errors.As(err, &unserved) {
		t.Error("the first-party client reinterpreted a 404")
	}
}

// providerOn returns a provider whose Configure resolved p. The platform
// blocks that set it arrive with the platform branches; the gate only reads
// the resolved value.
func providerOn(p platform.Platform) *AnthropicProvider {
	prov := &AnthropicProvider{version: "test"}
	prov.platform.Store(p)
	return prov
}

const inviteReason = "The gateway serves no invite route."

// TestGateRefusesAnUnavailableResource: on the platform that does not serve
// it, the type is a stand-in whose Configure fails with the platform's name,
// the reason and the documentation link; its Metadata and Schema are the real
// type's, so the plan still decodes.
func TestGateRefusesAnUnavailableResource(t *testing.T) {
	ctx := context.Background()
	reg := registration[resource.Resource]{
		New: organizations.NewInviteResource,
		AWS: platform.Unavailable(inviteReason),
	}

	r := providerOn(platform.AWS).gateResource(reg)()
	if _, ok := r.(*refusedResource); !ok {
		t.Fatalf("factory returned %T, want the unavailable stand-in", r)
	}
	if got := resourceTypeName(r); got != "anthropic_invite" {
		t.Errorf("stand-in type name = %q", got)
	}
	var got, want resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &got)
	organizations.NewInviteResource().Schema(ctx, resource.SchemaRequest{}, &want)
	// Validators and plan modifiers hold funcs, which DeepEqual never finds
	// equal; the Terraform type is what decoding a plan depends on.
	if !got.Schema.Type().TerraformType(ctx).Equal(want.Schema.Type().TerraformType(ctx)) {
		t.Error("the stand-in's schema type differs from the real type's")
	}

	cr, ok := r.(resource.ResourceWithConfigure)
	if !ok {
		t.Fatal("stand-in does not implement Configure")
	}
	var resp resource.ConfigureResponse
	cr.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerdata.ProviderData{Platform: platform.AWS}}, &resp)
	assertUnavailable(t, resp.Diagnostics.Errors(), "anthropic_invite", platform.AWS, inviteReason)
}

func TestGateRefusesAnUnavailableDataSource(t *testing.T) {
	ctx := context.Background()
	reg := registration[datasource.DataSource]{
		New:         organizations.NewInvitesDataSource,
		GoogleCloud: platform.Unavailable(inviteReason),
	}

	d := providerOn(platform.GoogleCloud).gateDataSource(reg)()
	cd, ok := d.(datasource.DataSourceWithConfigure)
	if _, stand := d.(*refusedDataSource); !stand || !ok {
		t.Fatalf("factory returned %T, want the unavailable stand-in", d)
	}
	var resp datasource.ConfigureResponse
	cd.Configure(ctx, datasource.ConfigureRequest{ProviderData: &providerdata.ProviderData{Platform: platform.GoogleCloud}}, &resp)
	assertUnavailable(t, resp.Diagnostics.Errors(), "anthropic_invites", platform.GoogleCloud, inviteReason)
}

// TestGateAllowsWhatIsNotUnavailable: supported, unverified and undeclared
// types are the real type on every platform, and every type is first party.
func TestGateAllowsWhatIsNotUnavailable(t *testing.T) {
	want := reflect.TypeOf(organizations.NewInviteResource())
	tests := []struct {
		name     string
		platform platform.Platform
		reg      registration[resource.Resource]
	}{
		{"supported", platform.AWS, registration[resource.Resource]{New: organizations.NewInviteResource, AWS: platform.Supported()}},
		{"unverified", platform.AWS, registration[resource.Resource]{New: organizations.NewInviteResource, AWS: platform.Unverified("open")}},
		{"undeclared", platform.GoogleCloud, registration[resource.Resource]{New: organizations.NewInviteResource, AWS: platform.Unavailable("x")}},
		{"first party", platform.FirstParty, registration[resource.Resource]{
			New: organizations.NewInviteResource, AWS: platform.Unavailable("x"), GoogleCloud: platform.Unavailable("x"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reflect.TypeOf(providerOn(tt.platform).gateResource(tt.reg)()); got != want {
				t.Errorf("factory returned %v, want %v", got, want)
			}
		})
	}
}

func assertUnavailable(t *testing.T, errs diag.Diagnostics, typeName string, p platform.Platform, reason string) {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if want := typeName + " Not Available on " + p.DisplayName(); errs[0].Summary() != want {
		t.Errorf("summary = %q, want %q", errs[0].Summary(), want)
	}
	for _, want := range []string{typeName, p.DisplayName(), reason, p.DocURL()} {
		if !strings.Contains(errs[0].Detail(), want) {
			t.Errorf("detail %q does not contain %q", errs[0].Detail(), want)
		}
	}
}

// TestProviderRegistersValidTypes walks what the provider serves: its own
// name and version, then every resource and data source, which must have a
// unique anthropic_ type name and a schema the framework accepts. A broken
// schema otherwise only shows when an acceptance test or a user loads it.
func TestProviderRegistersValidTypes(t *testing.T) {
	ctx := context.Background()
	p, ok := New("1.2.3")().(*AnthropicProvider)
	if !ok {
		t.Fatal("New does not build an *AnthropicProvider")
	}
	var meta provider.MetadataResponse
	p.Metadata(ctx, provider.MetadataRequest{}, &meta)
	if meta.TypeName != "anthropic" || meta.Version != "1.2.3" {
		t.Errorf("Metadata = %q %q, want anthropic 1.2.3", meta.TypeName, meta.Version)
	}

	seen := map[string]bool{}
	check := func(kind, name string) {
		t.Helper()
		if !strings.HasPrefix(name, "anthropic_") {
			t.Errorf("%s type name %q does not start with anthropic_", kind, name)
		}
		if seen[kind+" "+name] {
			t.Errorf("%s %s is registered twice", kind, name)
		}
		seen[kind+" "+name] = true
	}

	resources := p.Resources(ctx)
	for _, factory := range resources {
		r := factory()
		var m resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: meta.TypeName}, &m)
		check("resource", m.TypeName)
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		s.Diagnostics.Append(s.Schema.ValidateImplementation(ctx)...)
		if s.Diagnostics.HasError() {
			t.Errorf("resource %s: invalid schema: %v", m.TypeName, s.Diagnostics)
		}
	}

	dataSources := p.DataSources(ctx)
	for _, factory := range dataSources {
		d := factory()
		var m datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: meta.TypeName}, &m)
		check("data source", m.TypeName)
		var s datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &s)
		s.Diagnostics.Append(s.Schema.ValidateImplementation(ctx)...)
		if s.Diagnostics.HasError() {
			t.Errorf("data source %s: invalid schema: %v", m.TypeName, s.Diagnostics)
		}
	}

	if len(resources) == 0 || len(dataSources) == 0 {
		t.Errorf("%d resources and %d data sources registered", len(resources), len(dataSources))
	}
}
