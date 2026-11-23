// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package schematest

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// testResource and testDataSource declare one attribute of each type the
// builders convert.
type testResource struct{}

func (testResource) Metadata(context.Context, resource.MetadataRequest, *resource.MetadataResponse) {}
func (testResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"name":  schema.StringAttribute{Optional: true},
		"flag":  schema.BoolAttribute{Optional: true},
		"count": schema.Int64Attribute{Optional: true},
		"ratio": schema.Float64Attribute{Optional: true},
		"files": schema.ListAttribute{Optional: true, ElementType: types.StringType},
		"ids":   schema.SetAttribute{Optional: true, ElementType: types.StringType},
		"tags":  schema.MapAttribute{Optional: true, ElementType: types.StringType},
	}}
}
func (testResource) Create(context.Context, resource.CreateRequest, *resource.CreateResponse) {}
func (testResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse)       {}
func (testResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}
func (testResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

type testDataSource struct{}

func (testDataSource) Metadata(context.Context, datasource.MetadataRequest, *datasource.MetadataResponse) {
}
func (testDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{Attributes: map[string]dsschema.Attribute{
		"id":    dsschema.StringAttribute{Required: true},
		"items": dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
	}}
}
func (testDataSource) Read(context.Context, datasource.ReadRequest, *datasource.ReadResponse) {}

type model struct {
	Name  types.String  `tfsdk:"name"`
	Flag  types.Bool    `tfsdk:"flag"`
	Count types.Int64   `tfsdk:"count"`
	Ratio types.Float64 `tfsdk:"ratio"`
	Files types.List    `tfsdk:"files"`
	IDs   types.Set     `tfsdk:"ids"`
	Tags  types.Map     `tfsdk:"tags"`
}

func TestBuildersConvertGoValues(t *testing.T) {
	ctx := context.Background()
	overrides := map[string]any{
		"name":  "n",
		"flag":  true,
		"count": 3,
		"ratio": 0.5,
		"files": []string{"a", "b"},
		"ids":   []string{"x"},
		"tags":  map[string]string{"k": "v"},
	}
	var fromPlan, fromState, fromConfig model
	if d := Plan(t, testResource{}, overrides).Get(ctx, &fromPlan); d.HasError() {
		t.Fatal(d)
	}
	if d := State(t, testResource{}, overrides).Get(ctx, &fromState); d.HasError() {
		t.Fatal(d)
	}
	if d := Config(t, testResource{}, overrides).Get(ctx, &fromConfig); d.HasError() {
		t.Fatal(d)
	}
	for _, m := range []model{fromPlan, fromState, fromConfig} {
		var files, ids []string
		m.Files.ElementsAs(ctx, &files, false)
		m.IDs.ElementsAs(ctx, &ids, false)
		tags := map[string]string{}
		m.Tags.ElementsAs(ctx, &tags, false)
		if m.Name.ValueString() != "n" || !m.Flag.ValueBool() || m.Count.ValueInt64() != 3 || m.Ratio.ValueFloat64() != 0.5 ||
			len(files) != 2 || files[1] != "b" || len(ids) != 1 || tags["k"] != "v" {
			t.Errorf("decoded %+v", m)
		}
	}
}

func TestBuildersNullUnknownAndRawValues(t *testing.T) {
	ctx := context.Background()
	var m model
	if d := Plan(t, testResource{}, map[string]any{
		"name":  Unknown,
		"flag":  nil,
		"count": int64(7),
		"ratio": tftypes.NewValue(tftypes.Number, big.NewFloat(1.5)),
	}).Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	if !m.Name.IsUnknown() || !m.Flag.IsNull() || m.Count.ValueInt64() != 7 || m.Ratio.ValueFloat64() != 1.5 || !m.Files.IsNull() {
		t.Errorf("decoded %+v", m)
	}
	if v := Value(t, testResource{}, nil); !v.Type().Is(ResourceObjectType(t, testResource{})) {
		t.Errorf("Value type %s", v.Type())
	}
}

func TestBuildersRejectMismatches(t *testing.T) {
	for name, overrides := range map[string]map[string]any{
		"unknown attribute":   {"nope": "x"},
		"wrong raw type":      {"name": tftypes.NewValue(tftypes.Bool, true)},
		"[]string for a map":  {"tags": []string{"a"}},
		"map for a list":      {"files": map[string]string{"a": "b"}},
		"string for a number": {"count": "three"},
	} {
		ft := &fatalT{T: t}
		func() {
			defer func() { _ = recover() }()
			Value(ft, testResource{}, overrides)
		}()
		if !ft.failed {
			t.Errorf("%s: Value accepted %v", name, overrides)
		}
	}
}

// fatalT records a Fatalf and stops the builder like the real one would.
type fatalT struct {
	*testing.T
	failed bool
}

func (f *fatalT) Helper() {}
func (f *fatalT) Fatalf(string, ...any) {
	f.failed = true
	panic("fatal")
}

func TestDataSourceTwins(t *testing.T) {
	ctx := context.Background()
	d := testDataSource{}
	if got := DataSourceSchema(t, d).Attributes["id"]; got == nil || !got.IsRequired() {
		t.Error("DataSourceSchema lost the id attribute")
	}
	if n := len(DataSourceNullValues(t, d)); n != 2 {
		t.Errorf("DataSourceNullValues has %d values", n)
	}
	var id types.String
	if diags := NullDataSourceConfig(t, d).GetAttribute(ctx, path.Root("id"), &id); diags.HasError() || !id.IsNull() {
		t.Errorf("NullDataSourceConfig id = %v %v", id, diags)
	}
	if diags := DataSourceConfig(t, d, map[string]any{"id": "x"}).GetAttribute(ctx, path.Root("id"), &id); diags.HasError() || id.ValueString() != "x" {
		t.Errorf("DataSourceConfig id = %v %v", id, diags)
	}
}

func TestHasErrorDetail(t *testing.T) {
	var diags diag.Diagnostics
	diags.AddWarning("Warn", "the needle in a warning")
	if HasErrorDetail(diags, "needle") {
		t.Error("a warning counted as an error")
	}
	diags.AddError("Client Error", "Unable to read thing: 404")
	if !HasErrorDetail(diags, "404") || !HasErrorDetail(diags, "Client Error") || HasErrorDetail(diags, "500") {
		t.Error("HasErrorDetail misses or invents an error")
	}
}
