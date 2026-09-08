// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package schematest provides shared test helpers for building a hand-made
// tfsdk.State from a resource schema, which ImportState unit tests need
// because resp.State.SetAttribute requires an existing Raw value tree.
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
func ResourceSchema(t *testing.T, r resource.Resource) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// ResourceObjectType returns the underlying tftypes.Object type of r's schema.
func ResourceObjectType(t *testing.T, r resource.Resource) tftypes.Object {
	t.Helper()
	tfType, ok := ResourceSchema(t, r).Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	obj, ok := tfType.TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("schema type is not a tftypes.Object")
	}
	return obj
}

// NullValues returns a null tftypes.Value for every top-level attribute of r's
// schema, ready to be passed to tftypes.NewValue(ResourceObjectType(t, r), ...)
// after overriding the attributes a test wants to set.
func NullValues(t *testing.T, r resource.Resource) map[string]tftypes.Value {
	t.Helper()
	obj := ResourceObjectType(t, r)
	vals := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, typ := range obj.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

// NullState returns a tfsdk.State for r whose every attribute is null.
func NullState(t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()
	return tfsdk.State{
		Raw:    tftypes.NewValue(ResourceObjectType(t, r), NullValues(t, r)),
		Schema: ResourceSchema(t, r),
	}
}
