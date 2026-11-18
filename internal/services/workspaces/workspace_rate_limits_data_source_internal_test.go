// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// runWorkspaceRateLimitsRead drives WorkspaceRateLimitsDataSource.Read for
// workspace wrkspc_01WS with the given optional filters.
func runWorkspaceRateLimitsRead(t *testing.T, srv *httptest.Server, groupType string, includeInherited *bool) (*datasource.ReadResponse, WorkspaceRateLimitsDataSourceModel) {
	t.Helper()

	d := &WorkspaceRateLimitsDataSource{adminClient: newTestAdminClient(t, srv)}
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)

	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["workspace_id"] = tftypes.NewValue(tftypes.String, "wrkspc_01WS")
	if groupType != "" {
		vals["group_type"] = tftypes.NewValue(tftypes.String, groupType)
	}
	if includeInherited != nil {
		vals["include_inherited"] = tftypes.NewValue(tftypes.Bool, *includeInherited)
	}

	req := datasource.ReadRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schemaResp.Schema,
	}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)

	var got WorkspaceRateLimitsDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	}
	return resp, got
}

// An override entry (source workspace) and an entry the workspace only
// inherits (no override at all, source organization), as returned with
// include_inherited=true.
const rateLimitsInheritedPage = `{"data":[
	{
		"type": "workspace_rate_limit",
		"rate_limit_id": "rl_01SONNET",
		"workspace_id": "wrkspc_01WS",
		"group": {"id": "rlg_01SONNET", "type": "model_group", "display_name": "Claude Sonnet 4.x"},
		"group_type": "model_group",
		"models": ["claude-sonnet-4-5"],
		"limits": [
			{"type": "requests_per_minute", "value": 100, "org_limit": 4000, "source": {"type": "workspace"}},
			{"type": "output_tokens_per_minute", "value": 80000, "org_limit": 80000, "source": {"type": "organization"}}
		]
	},
	{
		"type": "workspace_rate_limit",
		"rate_limit_id": "rl_01BATCH",
		"workspace_id": "wrkspc_01WS",
		"group": {"id": "rlg_01BATCH", "type": "batch"},
		"group_type": "batch",
		"models": null,
		"limits": [
			{"type": "enqueued_batch_requests", "value": 500000, "org_limit": 500000, "source": {"type": "organization"}}
		]
	}
],"next_page":null}`

func TestWorkspaceRateLimitsDataSourceRead_includeInheritedKeepsInheritedEntries(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/workspaces/wrkspc_01WS/rate_limits" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		query = r.URL.Query()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, rateLimitsInheritedPage)
	}))
	defer srv.Close()

	includeInherited := true
	resp, got := runWorkspaceRateLimitsRead(t, srv, "", &includeInherited)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if got := query.Get("include_inherited"); got != "true" {
		t.Errorf("include_inherited = %q, want true", got)
	}

	entries := got.RateLimits.Elements()
	if len(entries) != 2 {
		t.Fatalf("rate_limits length = %d, want 2 (the inherited-only batch entry must be kept)", len(entries))
	}

	sonnet := entries[0].(types.Object).Attributes()
	if got := sonnet["rate_limit_id"].(types.String).ValueString(); got != "rl_01SONNET" {
		t.Errorf("rate_limit_id = %q, want rl_01SONNET", got)
	}
	group := sonnet["group"].(types.Object).Attributes()
	if got := group["id"].(types.String).ValueString(); got != "rlg_01SONNET" {
		t.Errorf("group.id = %q, want rlg_01SONNET", got)
	}
	if got := group["type"].(types.String).ValueString(); got != "model_group" {
		t.Errorf("group.type = %q, want model_group", got)
	}
	if got := group["display_name"].(types.String).ValueString(); got != "Claude Sonnet 4.x" {
		t.Errorf("group.display_name = %q, want Claude Sonnet 4.x", got)
	}
	if got := sonnet["group_type"].(types.String).ValueString(); got != "model_group" {
		t.Errorf("group_type = %q, want model_group", got)
	}

	limits := sonnet["limits"].(types.List).Elements()
	if len(limits) != 2 {
		t.Fatalf("sonnet limits length = %d, want 2", len(limits))
	}
	wantSources := []string{"workspace", "organization"}
	for i, l := range limits {
		source := l.(types.Object).Attributes()["source"].(types.Object).Attributes()
		if got := source["type"].(types.String).ValueString(); got != wantSources[i] {
			t.Errorf("limits[%d].source.type = %q, want %q", i, got, wantSources[i])
		}
	}

	batch := entries[1].(types.Object).Attributes()
	if got := batch["group"].(types.Object).Attributes()["display_name"].(types.String); !got.IsNull() {
		t.Errorf("batch group.display_name = %v, want null", got)
	}
	if got := batch["models"].(types.List); !got.IsNull() {
		t.Errorf("batch models = %v, want null", got)
	}
}

func TestWorkspaceRateLimitsDataSourceRead_followsNextPage(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		w.Header().Set("content-type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[{"type":"workspace_rate_limit","rate_limit_id":"rl_01A","group":{"id":"rlg_01A","type":"files"},"group_type":"files","models":null,"limits":[]}],"next_page":"cursor_2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"type":"workspace_rate_limit","rate_limit_id":"rl_01B","group":{"id":"rlg_01B","type":"files"},"group_type":"files","models":null,"limits":[]}],"next_page":null}`)
	}))
	defer srv.Close()

	resp, got := runWorkspaceRateLimitsRead(t, srv, "files", nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if n := len(got.RateLimits.Elements()); n != 2 {
		t.Errorf("rate_limits length = %d, want 2", n)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	for i, q := range queries {
		if got := q.Get("group_type"); got != "files" {
			t.Errorf("request %d group_type = %q, want files", i, got)
		}
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("request %d limit = %q, want 1000", i, got)
		}
		if _, ok := q["after_id"]; ok {
			t.Errorf("request %d sent after_id, which this endpoint does not paginate with", i)
		}
		if _, ok := q["include_inherited"]; ok {
			t.Errorf("request %d sent include_inherited although it is unset", i)
		}
	}
	if _, ok := queries[0]["page"]; ok {
		t.Errorf("first request must not send page, got %v", queries[0]["page"])
	}
	if got := queries[1].Get("page"); got != "cursor_2" {
		t.Errorf("second request page = %q, want cursor_2", got)
	}
}

// A response that carries no group object (an older payload) maps to a null
// group rather than to an object of empty strings, and a missing source maps
// to a null source.
func TestMapRateLimitToObject_withoutGroupOrSource(t *testing.T) {
	item := workspaceRateLimitAPIItem{
		Type:        "workspace_rate_limit",
		RateLimitID: "rl_01A",
		GroupType:   "model_group",
		Models:      []string{"claude-sonnet-4-5"},
		Limits:      []workspaceRateLimitEntry{{Type: "requests_per_minute", Value: 10}},
	}
	obj, diags := mapRateLimitToObject(context.Background(), item)
	if diags.HasError() {
		t.Fatalf("mapRateLimitToObject: %v", diags)
	}
	attrs := obj.(types.Object).Attributes()
	if g := attrs["group"].(types.Object); !g.IsNull() {
		t.Errorf("group = %v, want null", g)
	}
	limit := attrs["limits"].(types.List).Elements()[0].(types.Object).Attributes()
	if s := limit["source"].(types.Object); !s.IsNull() {
		t.Errorf("source = %v, want null", s)
	}
	if o := limit["org_limit"].(types.Int64); !o.IsNull() {
		t.Errorf("org_limit = %v, want null", o)
	}
}

func TestWorkspaceRateLimitsFilters(t *testing.T) {
	if got := workspaceRateLimitsFilters("", false).Encode(); got != "" {
		t.Errorf("no filters: %q, want empty", got)
	}
	got := workspaceRateLimitsFilters("model_group", true).Encode()
	if want := "group_type=model_group&include_inherited=true"; got != want {
		t.Errorf("filters = %q, want %q", got, want)
	}
}

// listRateLimitPages must not mutate the caller's filters while it adds the
// page size and the cursor to each request.
func TestListRateLimitPages_keepsFiltersIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[{"rate_limit_id":"rl_01A"}],"next_page":"cursor_2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"rate_limit_id":"rl_01B"}],"next_page":""}`)
	}))
	defer srv.Close()

	filters := url.Values{"group_type": {"batch"}}
	items, err := listRateLimitPages[workspaceRateLimitAPIItem](context.Background(), newTestAdminClient(t, srv), "/v1/organizations/workspaces/wrkspc_01WS/rate_limits", filters)
	if err != nil {
		t.Fatalf("listRateLimitPages: %v", err)
	}
	if len(items) != 2 || items[1].RateLimitID != "rl_01B" {
		t.Errorf("items = %+v, want rl_01A then rl_01B", items)
	}
	if got := filters.Encode(); got != "group_type=batch" {
		t.Errorf("filters mutated to %q", got)
	}
}

// A next_page the listing already sent, the one just sent or an earlier one,
// fails the read instead of looping. The fake stops answering with a cursor
// after a few requests, so the test ends even without the guard.
func TestListRateLimitPages_repeatedCursorFails(t *testing.T) {
	cases := []struct {
		name     string
		next     map[string]string // page sent -> next_page returned
		requests int
	}{
		{name: "echoed cursor", next: map[string]string{"": "cursor_2", "cursor_2": "cursor_2"}, requests: 2},
		{name: "cycle", next: map[string]string{"": "cursor_2", "cursor_2": "cursor_3", "cursor_3": "cursor_2"}, requests: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				next := `"` + tc.next[r.URL.Query().Get("page")] + `"`
				if requests >= 6 {
					next = "null"
				}
				w.Header().Set("content-type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"rate_limit_id":"rl_01A"}],"next_page":`+next+`}`)
			}))
			defer srv.Close()

			items, err := listRateLimitPages[workspaceRateLimitAPIItem](context.Background(), newTestAdminClient(t, srv), "/v1/organizations/workspaces/wrkspc_01WS/rate_limits", nil)
			if err == nil {
				t.Fatalf("listRateLimitPages returned %d items and no error, want a repeated cursor error", len(items))
			}
			if !strings.Contains(err.Error(), "cursor already sent") {
				t.Errorf("err = %v, want a repeated cursor error", err)
			}
			if requests != tc.requests {
				t.Errorf("requests = %d, want %d", requests, tc.requests)
			}
		})
	}
}

// Identifiers and types the API leaves out map to null, not to "": an empty
// rate_limit_id would otherwise look like a real ID.
func TestMapRateLimitToObject_absentStringsMapToNull(t *testing.T) {
	item := workspaceRateLimitAPIItem{
		Type:   "workspace_rate_limit",
		Group:  &rateLimitGroupAPI{},
		Limits: []workspaceRateLimitEntry{{Type: "requests_per_minute", Value: 10, Source: &workspaceRateLimitSource{}}},
	}
	obj, diags := mapRateLimitToObject(context.Background(), item)
	if diags.HasError() {
		t.Fatalf("mapRateLimitToObject: %v", diags)
	}
	attrs := obj.(types.Object).Attributes()
	if id := attrs["rate_limit_id"].(types.String); !id.IsNull() {
		t.Errorf("rate_limit_id = %v, want null", id)
	}
	group := attrs["group"].(types.Object).Attributes()
	for _, name := range []string{"id", "type"} {
		if v := group[name].(types.String); !v.IsNull() {
			t.Errorf("group.%s = %v, want null", name, v)
		}
	}
	source := attrs["limits"].(types.List).Elements()[0].(types.Object).Attributes()["source"].(types.Object).Attributes()
	if v := source["type"].(types.String); !v.IsNull() {
		t.Errorf("source.type = %v, want null", v)
	}
}
