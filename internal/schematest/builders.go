// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package schematest

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Unknown, as an override value, makes the attribute unknown (a value known
// only after apply).
var Unknown = unknownMarker{}

type unknownMarker struct{}

// DataSourceSchema returns the schema d describes.
func DataSourceSchema(tb testing.TB, d datasource.DataSource) dsschema.Schema {
	tb.Helper()
	var resp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		tb.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// DataSourceObjectType returns the underlying tftypes.Object type of d's
// schema.
func DataSourceObjectType(tb testing.TB, d datasource.DataSource) tftypes.Object {
	tb.Helper()
	obj, ok := DataSourceSchema(tb, d).Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		tb.Fatal("schema type is not a tftypes.Object")
	}
	return obj
}

// DataSourceNullValues is NullValues for a data source.
func DataSourceNullValues(tb testing.TB, d datasource.DataSource) map[string]tftypes.Value {
	tb.Helper()
	return nullValues(DataSourceObjectType(tb, d))
}

// NullDataSourceConfig returns a tfsdk.Config for d whose every attribute is
// null.
func NullDataSourceConfig(tb testing.TB, d datasource.DataSource) tfsdk.Config {
	tb.Helper()
	return DataSourceConfig(tb, d, nil)
}

// DataSourceConfig returns a tfsdk.Config for d: every top-level attribute
// null, except overrides (see Value for the accepted values).
func DataSourceConfig(tb testing.TB, d datasource.DataSource, overrides map[string]any) tfsdk.Config {
	tb.Helper()
	return tfsdk.Config{
		Raw:    objectValue(tb, DataSourceObjectType(tb, d), overrides),
		Schema: DataSourceSchema(tb, d),
	}
}

// Value returns an object value of r's schema: every top-level attribute
// null, except the ones in overrides. An override is a tftypes.Value used as
// is, nil (null), Unknown, or a Go value converted to the attribute's type:
// string, bool, int, int64, float64, []string (list or set of strings) or
// map[string]string (map of strings).
func Value(tb testing.TB, r resource.Resource, overrides map[string]any) tftypes.Value {
	tb.Helper()
	return objectValue(tb, ResourceObjectType(tb, r), overrides)
}

// Config returns a tfsdk.Config for r built like Value.
func Config(tb testing.TB, r resource.Resource, overrides map[string]any) tfsdk.Config {
	tb.Helper()
	return tfsdk.Config{Raw: Value(tb, r, overrides), Schema: ResourceSchema(tb, r)}
}

// Plan returns a tfsdk.Plan for r built like Value.
func Plan(tb testing.TB, r resource.Resource, overrides map[string]any) tfsdk.Plan {
	tb.Helper()
	return tfsdk.Plan{Raw: Value(tb, r, overrides), Schema: ResourceSchema(tb, r)}
}

// State returns a tfsdk.State for r built like Value.
func State(tb testing.TB, r resource.Resource, overrides map[string]any) tfsdk.State {
	tb.Helper()
	return tfsdk.State{Raw: Value(tb, r, overrides), Schema: ResourceSchema(tb, r)}
}

// HasErrorDetail reports whether diags holds an error whose summary or
// detail contains substr.
func HasErrorDetail(diags diag.Diagnostics, substr string) bool {
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), substr) || strings.Contains(d.Detail(), substr) {
			return true
		}
	}
	return false
}

func nullValues(obj tftypes.Object) map[string]tftypes.Value {
	vals := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, typ := range obj.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

func objectValue(tb testing.TB, obj tftypes.Object, overrides map[string]any) tftypes.Value {
	tb.Helper()
	vals := nullValues(obj)
	for name, v := range overrides {
		typ, ok := obj.AttributeTypes[name]
		if !ok {
			tb.Fatalf("schematest: the schema has no attribute %q", name)
		}
		tv, err := toValue(typ, v)
		if err != nil {
			tb.Fatalf("schematest: attribute %q: %v", name, err)
		}
		vals[name] = tv
	}
	return tftypes.NewValue(obj, vals)
}

func toValue(typ tftypes.Type, v any) (tftypes.Value, error) {
	switch x := v.(type) {
	case tftypes.Value:
		if !x.Type().Equal(typ) {
			return tftypes.Value{}, fmt.Errorf("value of type %s, want %s", x.Type(), typ)
		}
		return x, nil
	case nil:
		return tftypes.NewValue(typ, nil), nil
	case unknownMarker:
		return tftypes.NewValue(typ, tftypes.UnknownValue), nil
	case int:
		return toValue(typ, big.NewFloat(float64(x)))
	case int64:
		return toValue(typ, big.NewFloat(float64(x)))
	case float64:
		return toValue(typ, big.NewFloat(x))
	case []string:
		elems := make([]tftypes.Value, len(x))
		for i, s := range x {
			elems[i] = tftypes.NewValue(tftypes.String, s)
		}
		if !typ.Is(tftypes.List{ElementType: tftypes.String}) && !typ.Is(tftypes.Set{ElementType: tftypes.String}) {
			return tftypes.Value{}, fmt.Errorf("[]string for a %s attribute", typ)
		}
		return tftypes.NewValue(typ, elems), nil
	case map[string]string:
		if !typ.Is(tftypes.Map{ElementType: tftypes.String}) {
			return tftypes.Value{}, fmt.Errorf("map[string]string for a %s attribute", typ)
		}
		elems := make(map[string]tftypes.Value, len(x))
		for k, s := range x {
			elems[k] = tftypes.NewValue(tftypes.String, s)
		}
		return tftypes.NewValue(typ, elems), nil
	}
	if err := tftypes.ValidateValue(typ, v); err != nil {
		return tftypes.Value{}, err
	}
	return tftypes.NewValue(typ, v), nil
}
