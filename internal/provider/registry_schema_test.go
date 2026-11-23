// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestProviderRegistersValidTypes walks what the provider serves: its own
// name and version, then every resource and data source, which must have a
// schema the framework accepts. A broken schema otherwise only shows when an
// acceptance test or a user loads it. TestRegistrationsNameEveryTypeOnce
// checks the type names.
func TestProviderRegistersValidTypes(t *testing.T) {
	ctx := context.Background()
	p, ok := New("1.2.3")().(*AnthropicProvider)
	if !ok {
		t.Fatal("New does not build an *AnthropicProvider")
	}
	var meta provider.MetadataResponse
	p.Metadata(ctx, provider.MetadataRequest{}, &meta)
	if meta.TypeName != providerTypeName || meta.Version != "1.2.3" {
		t.Errorf("Metadata = %q %q, want %s 1.2.3", meta.TypeName, meta.Version, providerTypeName)
	}

	resources := p.Resources(ctx)
	for _, factory := range resources {
		r := factory()
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		s.Diagnostics.Append(s.Schema.ValidateImplementation(ctx)...)
		if s.Diagnostics.HasError() {
			t.Errorf("resource %s: invalid schema: %v", resourceTypeName(r), s.Diagnostics)
		}
	}

	dataSources := p.DataSources(ctx)
	for _, factory := range dataSources {
		d := factory()
		var s datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &s)
		s.Diagnostics.Append(s.Schema.ValidateImplementation(ctx)...)
		if s.Diagnostics.HasError() {
			t.Errorf("data source %s: invalid schema: %v", dataSourceTypeName(d), s.Diagnostics)
		}
	}

	if len(resources) == 0 || len(dataSources) == 0 {
		t.Errorf("%d resources and %d data sources registered", len(resources), len(dataSources))
	}
}
