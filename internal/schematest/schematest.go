// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package schematest provides shared test helpers for building a hand-made
// tfsdk.Config, Plan or State from a resource or data source schema, for unit
// tests that call a method directly: ImportState tests need a null state
// because resp.State.SetAttribute requires an existing Raw value tree, and
// Create/Read/Update tests need a plan or state with a few attributes set
// (Value, Config, Plan, State and their data source twins in builders.go).
package schematest

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ResourceSchema returns the schema r describes.
func ResourceSchema(tb testing.TB, r resource.Resource) schema.Schema {
	tb.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		tb.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// ResourceObjectType returns the underlying tftypes.Object type of r's schema.
func ResourceObjectType(tb testing.TB, r resource.Resource) tftypes.Object {
	tb.Helper()
	tfType, ok := ResourceSchema(tb, r).Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		tb.Fatal("schema type does not implement TerraformType")
	}
	obj, ok := tfType.TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		tb.Fatal("schema type is not a tftypes.Object")
	}
	return obj
}

// NullValues returns a null tftypes.Value for every top-level attribute of r's
// schema, ready to be passed to tftypes.NewValue(ResourceObjectType(t, r), ...)
// after overriding the attributes a test wants to set.
func NullValues(tb testing.TB, r resource.Resource) map[string]tftypes.Value {
	tb.Helper()
	return nullValues(ResourceObjectType(tb, r))
}

// NullState returns a tfsdk.State for r whose every attribute is null.
func NullState(tb testing.TB, r resource.Resource) tfsdk.State {
	tb.Helper()
	return tfsdk.State{
		Raw:    tftypes.NewValue(ResourceObjectType(tb, r), NullValues(tb, r)),
		Schema: ResourceSchema(tb, r),
	}
}
