// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapMemoryDSToListObject_withAndWithoutContent(t *testing.T) {
	// The JSON.Content validity flag is set by the unmarshaler, not by
	// assigning the Go Content field directly, so build these fixtures by
	// round-tripping through JSON.
	var full anthropic.BetaManagedAgentsMemory
	if err := full.UnmarshalJSON([]byte(`{"id":"mem_01ABC","content_sha256":"abcd","content_size_bytes":5,"created_at":"2024-01-15T10:00:00Z","memory_store_id":"memstore_01ABC","memory_version_id":"memver_01ABC","path":"/notes/foo.md","type":"memory","updated_at":"2024-01-15T10:00:00Z","content":"hello"}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fullObj, diags := mapMemoryDSToListObject(&full)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fullAttrs := fullObj.(types.Object).Attributes()
	if fullAttrs["content"].(types.String).IsNull() {
		t.Error("expected content to be non-null when the API returns content")
	}
	if got := fullAttrs["content"].(types.String).ValueString(); got != "hello" {
		t.Errorf("content = %q, want hello", got)
	}

	var basic anthropic.BetaManagedAgentsMemory
	if err := basic.UnmarshalJSON([]byte(`{"id":"mem_02DEF","content_sha256":"efgh","content_size_bytes":0,"created_at":"2024-01-15T10:00:00Z","memory_store_id":"memstore_01ABC","memory_version_id":"memver_02DEF","path":"/notes/bar.md","type":"memory","updated_at":"2024-01-15T10:00:00Z","content":null}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	basicObj, diags := mapMemoryDSToListObject(&basic)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	basicAttrs := basicObj.(types.Object).Attributes()
	if !basicAttrs["content"].(types.String).IsNull() {
		t.Errorf("expected content to be null, got %q", basicAttrs["content"].(types.String).ValueString())
	}
}

const memoryJSONTemplate = `{
	"id": "%s",
	"content_sha256": "abcd",
	"content_size_bytes": 5,
	"created_at": "2024-01-15T10:00:00Z",
	"memory_store_id": "memstore_01ABC",
	"memory_version_id": "memver_01ABC",
	"path": "%s",
	"type": "memory",
	"updated_at": "2024-01-15T10:00:00Z",
	"content": null
}`

const memoryPrefixJSONTemplate = `{
	"path": "%s",
	"type": "memory_prefix"
}`

func sprintfMemory(id, path string) string {
	return fmt.Sprintf(memoryJSONTemplate, id, path)
}

func sprintfMemoryPrefix(path string) string {
	return fmt.Sprintf(memoryPrefixJSONTemplate, path)
}

// memoriesDataSourceSchema returns the schema MemoriesDataSource declares.
func memoriesDataSourceSchema(t *testing.T) dsschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&MemoriesDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// memoriesConfigObjectType returns the underlying tftypes.Object type of the
// MemoriesDataSource schema.
func memoriesConfigObjectType(t *testing.T) tftypes.Object {
	t.Helper()
	tfType, ok := memoriesDataSourceSchema(t).Type().(interface {
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

// memoriesNullConfigValues returns a null tftypes.Value for every top-level
// attribute of the MemoriesDataSource schema.
func memoriesNullConfigValues(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	obj := memoriesConfigObjectType(t)
	vals := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, typ := range obj.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

// readMemoriesDataSource drives MemoriesDataSource.Read directly (bypassing
// Configure) against client, with the given config attribute overrides
// applied on top of an otherwise-null config. This exercises the actual
// Read wiring (view selection, param forwarding, union split) rather than
// just the SDK call it makes.
func readMemoriesDataSource(t *testing.T, client *anthropic.Client, overrides map[string]tftypes.Value) (memoriesDSModel, datasource.ReadResponse) {
	t.Helper()
	sch := memoriesDataSourceSchema(t)
	objType := memoriesConfigObjectType(t)
	vals := memoriesNullConfigValues(t)
	for k, v := range overrides {
		vals[k] = v
	}

	d := &MemoriesDataSource{client: client}
	req := datasource.ReadRequest{
		Config: tfsdk.Config{
			Raw:    tftypes.NewValue(objType, vals),
			Schema: sch,
		},
	}
	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: sch},
	}
	d.Read(context.Background(), req, &resp)

	var data memoriesDSModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &data)...)
	}
	return data, resp
}

// TestMemoriesDataSourceRead_paginatesAndSplitsUnion exercises Read itself
// (not just the SDK call it makes) against a fake two-page server, asserting
// that both pages are fetched and that memory / memory_prefix items are
// routed into "memories" and "prefixes" respectively.
func TestMemoriesDataSourceRead_paginatesAndSplitsUnion(t *testing.T) {
	var gotQueries []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+sprintfMemory("mem_01ABC", "/notes/foo.md")+`,`+sprintfMemoryPrefix("/projects/")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+sprintfMemory("mem_02DEF", "/notes/bar.md")+`],"next_page":""}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	data, resp := readMemoriesDataSource(t, client, map[string]tftypes.Value{
		"memory_store_id": tftypes.NewValue(tftypes.String, "memstore_01ABC"),
		"path_prefix":     tftypes.NewValue(tftypes.String, "/notes/"),
		"depth":           tftypes.NewValue(tftypes.Number, 1),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	memories := data.Memories.Elements()
	prefixes := data.Prefixes.Elements()
	if len(memories) != 2 {
		t.Fatalf("expected 2 memories across 2 pages, got %d", len(memories))
	}
	if len(prefixes) != 1 || prefixes[0].(types.String).ValueString() != "/projects/" {
		t.Fatalf("expected 1 prefix /projects/, got %v", prefixes)
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("path_prefix"); got != "/notes/" {
		t.Errorf("first request path_prefix = %q, want /notes/", got)
	}
	if got := gotQueries[0].Get("depth"); got != "1" {
		t.Errorf("first request depth = %q, want 1", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

// TestMemoriesDataSourceRead_includeContentSelectsViewFull pins the wiring
// deleting `params.View = ...Full` in Read would silently break: setting
// include_content = true must send view=full.
func TestMemoriesDataSourceRead_includeContentSelectsViewFull(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	_, resp := readMemoriesDataSource(t, client, map[string]tftypes.Value{
		"memory_store_id": tftypes.NewValue(tftypes.String, "memstore_01ABC"),
		"include_content": tftypes.NewValue(tftypes.Bool, true),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	if got := gotQuery.Get("view"); got != "full" {
		t.Errorf("view = %q, want full", got)
	}
}

// TestMemoriesDataSourceRead_defaultOmitsViewAndFilters pins the opposite
// direction: without include_content (or path_prefix/depth), the request
// carries view=basic and omits the optional filters entirely.
func TestMemoriesDataSourceRead_defaultOmitsViewAndFilters(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	data, resp := readMemoriesDataSource(t, client, map[string]tftypes.Value{
		"memory_store_id": tftypes.NewValue(tftypes.String, "memstore_01ABC"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	if got := gotQuery.Get("view"); got != "basic" {
		t.Errorf("view = %q, want basic", got)
	}
	if _, present := gotQuery["path_prefix"]; present {
		t.Errorf("path_prefix sent as %q, want omitted", gotQuery.Get("path_prefix"))
	}
	if _, present := gotQuery["depth"]; present {
		t.Errorf("depth sent as %q, want omitted", gotQuery.Get("depth"))
	}
	if len(data.Memories.Elements()) != 0 {
		t.Errorf("expected 0 memories, got %d", len(data.Memories.Elements()))
	}
	if len(data.Prefixes.Elements()) != 0 {
		t.Errorf("expected 0 prefixes, got %d", len(data.Prefixes.Elements()))
	}
}

func TestMemoriesDataSourceRead_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"memory store not found"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	_, resp := readMemoriesDataSource(t, client, map[string]tftypes.Value{
		"memory_store_id": tftypes.NewValue(tftypes.String, "memstore_missing"),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a 404 response")
	}
}
