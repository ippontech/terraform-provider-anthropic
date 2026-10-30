// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live create/update/destroy acceptance tests for anthropic_workspace_rate_limit
// are intentionally not written: Admin API resource tests wait for a dedicated
// test organization (#58, see CLAUDE.md). Coverage here is httptest-based unit
// tests, plus the command = plan native test.
package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const (
	testRateLimitWorkspaceID = "wrkspc_01WS"
	testRateLimitID          = "rl_01SONNET"
	testRateLimitPath        = "/v1/organizations/workspaces/wrkspc_01WS/rate_limits/rl_01SONNET"
	testRateLimitsListPath   = "/v1/organizations/workspaces/wrkspc_01WS/rate_limits"
)

// rateLimitEntryJSON is the set-overrides response (and a list entry) for the
// Sonnet group, with the given limits JSON array.
func rateLimitEntryJSON(rateLimitID, limits string) string {
	return `{
		"type": "workspace_rate_limit",
		"rate_limit_id": "` + rateLimitID + `",
		"workspace_id": "wrkspc_01WS",
		"group": {"id": "rlg_01SONNET", "type": "model_group", "display_name": "Claude Sonnet 4.x"},
		"group_type": "model_group",
		"models": ["claude-sonnet-4-5"],
		"limits": ` + limits + `
	}`
}

// rateLimitModel builds a resource model; nil limits means null (an import).
func rateLimitModel(limits map[string]int64) WorkspaceRateLimitResourceModel {
	m := WorkspaceRateLimitResourceModel{
		ID:          types.StringValue(testRateLimitWorkspaceID + ":" + testRateLimitID),
		WorkspaceID: types.StringValue(testRateLimitWorkspaceID),
		RateLimitID: types.StringValue(testRateLimitID),
		Limits:      types.MapNull(types.Int64Type),
		Group:       types.ObjectNull(rateLimitGroupAttrTypes),
	}
	if limits != nil {
		elems := map[string]attr.Value{}
		for k, v := range limits {
			elems[k] = types.Int64Value(v)
		}
		m.Limits = types.MapValueMust(types.Int64Type, elems)
	}
	return m
}

func rateLimitState(t *testing.T, r *WorkspaceRateLimitResource, m WorkspaceRateLimitResourceModel) tfsdk.State {
	t.Helper()
	s := tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
	if diags := s.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return s
}

func rateLimitPlan(t *testing.T, r *WorkspaceRateLimitResource, m WorkspaceRateLimitResourceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: schematest.ResourceSchema(t, r)}
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("plan: %v", diags)
	}
	return p
}

// capturedRequest is what the stub server saw on the set-overrides endpoint.
type capturedRequest struct {
	method string
	path   string
	query  url.Values
	limits []map[string]any
}

// rateLimitServer answers the set-overrides POST with postResponse and the
// list GET with listResponse, recording every request.
func rateLimitServer(t *testing.T, postResponse, listResponse string, status int) (*httptest.Server, *[]capturedRequest) {
	t.Helper()
	var requests []capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := capturedRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		if r.Body != nil {
			var body struct {
				Limits []map[string]any `json:"limits"`
			}
			raw, _ := io.ReadAll(r.Body)
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Errorf("request body is not JSON: %v", err)
				}
				req.limits = body.Limits
			}
		}
		requests = append(requests, req)

		w.Header().Set("content-type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"not found"}}`)
			return
		}
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, postResponse)
			return
		}
		_, _ = io.WriteString(w, listResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// assertLimitsBody checks the request's limits array, in order; a nil want
// value means the entry must carry an explicit JSON null.
func assertLimitsBody(t *testing.T, got []map[string]any, want []struct {
	typ   string
	value *int64
}) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("limits = %v, want %d entries", got, len(want))
	}
	for i, w := range want {
		if got[i]["type"] != w.typ {
			t.Errorf("limits[%d].type = %v, want %s", i, got[i]["type"], w.typ)
		}
		v, present := got[i]["value"]
		if !present {
			t.Errorf("limits[%d] has no value key; a clear must send an explicit null", i)
			continue
		}
		if w.value == nil {
			if v != nil {
				t.Errorf("limits[%d].value = %v, want null", i, v)
			}
			continue
		}
		if f, ok := v.(float64); !ok || int64(f) != *w.value {
			t.Errorf("limits[%d].value = %v, want %d", i, v, *w.value)
		}
	}
}

func stateLimits(t *testing.T, s tfsdk.State) (map[string]int64, bool) {
	t.Helper()
	var m WorkspaceRateLimitResourceModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if m.Limits.IsNull() {
		return nil, true
	}
	out := map[string]int64{}
	if diags := m.Limits.ElementsAs(context.Background(), &out, false); diags.HasError() {
		t.Fatalf("limits: %v", diags)
	}
	return out, false
}

// --- Create ---

func TestWorkspaceRateLimitResource_CreateSendsPlannedLimits(t *testing.T) {
	srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID,
		`[{"type":"output_tokens_per_minute","value":20000,"org_limit":80000,"source":{"type":"workspace"}},
		  {"type":"requests_per_minute","value":100,"org_limit":4000,"source":{"type":"workspace"}}]`), "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	plan := rateLimitModel(map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 20000})
	plan.ID = types.StringUnknown()
	plan.Group = types.ObjectUnknown(rateLimitGroupAttrTypes)

	resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: rateLimitPlan(t, r, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
	req := (*requests)[0]
	if req.method != http.MethodPost || req.path != testRateLimitPath {
		t.Errorf("request = %s %s, want POST %s", req.method, req.path, testRateLimitPath)
	}
	assertLimitsBody(t, req.limits, []struct {
		typ   string
		value *int64
	}{
		{"output_tokens_per_minute", new(int64(20000))},
		{"requests_per_minute", new(int64(100))},
	})

	var got WorkspaceRateLimitResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got.ID.ValueString() != "wrkspc_01WS:rl_01SONNET" {
		t.Errorf("id = %q, want wrkspc_01WS:rl_01SONNET", got.ID.ValueString())
	}
	if dn := got.Group.Attributes()["display_name"].(types.String).ValueString(); dn != "Claude Sonnet 4.x" {
		t.Errorf("group.display_name = %q, want Claude Sonnet 4.x", dn)
	}
	if limits, _ := stateLimits(t, resp.State); limits["requests_per_minute"] != 100 || limits["output_tokens_per_minute"] != 20000 {
		t.Errorf("limits = %v, want the planned values", limits)
	}
}

// --- Update ---

func TestWorkspaceRateLimitResource_UpdateClearsRemovedTypes(t *testing.T) {
	srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID,
		`[{"type":"input_tokens_per_minute","value":5000,"org_limit":null,"source":{"type":"workspace"}},
		  {"type":"requests_per_minute","value":200,"org_limit":4000,"source":{"type":"workspace"}}]`), "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	state := rateLimitModel(map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 20000})
	plan := rateLimitModel(map[string]int64{"requests_per_minute": 200, "input_tokens_per_minute": 5000})

	resp := resource.UpdateResponse{State: rateLimitState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  rateLimitPlan(t, r, plan),
		State: rateLimitState(t, r, state),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	if len(*requests) != 1 || (*requests)[0].path != testRateLimitPath {
		t.Fatalf("requests = %+v, want one POST to %s", *requests, testRateLimitPath)
	}
	assertLimitsBody(t, (*requests)[0].limits, []struct {
		typ   string
		value *int64
	}{
		{"input_tokens_per_minute", new(int64(5000))},
		{"output_tokens_per_minute", nil},
		{"requests_per_minute", new(int64(200))},
	})

	limits, _ := stateLimits(t, resp.State)
	if len(limits) != 2 || limits["requests_per_minute"] != 200 || limits["input_tokens_per_minute"] != 5000 {
		t.Errorf("limits = %v, want the planned values", limits)
	}
}

// --- Delete ---

func TestWorkspaceRateLimitResource_DeleteClearsManagedTypes(t *testing.T) {
	srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID, `[]`), "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	state := rateLimitModel(map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 20000})
	resp := resource.DeleteResponse{State: rateLimitState(t, r, state)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}

	if len(*requests) != 1 || (*requests)[0].method != http.MethodPost || (*requests)[0].path != testRateLimitPath {
		t.Fatalf("requests = %+v, want one POST to %s", *requests, testRateLimitPath)
	}
	assertLimitsBody(t, (*requests)[0].limits, []struct {
		typ   string
		value *int64
	}{
		{"output_tokens_per_minute", nil},
		{"requests_per_minute", nil},
	})
}

func TestWorkspaceRateLimitResource_DeleteToleratesNotFound(t *testing.T) {
	srv, _ := rateLimitServer(t, "", "", http.StatusNotFound)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	state := rateLimitModel(map[string]int64{"requests_per_minute": 100})
	resp := resource.DeleteResponse{State: rateLimitState(t, r, state)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete on a gone workspace must succeed, got: %v", resp.Diagnostics)
	}
}

func TestWorkspaceRateLimitResource_DeleteWithNothingManagedSendsNothing(t *testing.T) {
	srv, requests := rateLimitServer(t, "", "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	state := rateLimitModel(map[string]int64{})
	resp := resource.DeleteResponse{State: rateLimitState(t, r, state)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}
	if len(*requests) != 0 {
		t.Errorf("requests = %d, want 0 (the endpoint rejects an empty limits list)", len(*requests))
	}
}

// --- Read ---

// The list holds the managed group with one managed override (changed out of
// band), one unmanaged override, and no trace of the managed tokens_per_day,
// which was cleared out of band; plus another group.
const rateLimitsListForRead = `{"data":[
	{"type":"workspace_rate_limit","rate_limit_id":"rl_01OTHER","workspace_id":"wrkspc_01WS",
	 "group":{"id":"rlg_01BATCH","type":"batch"},"group_type":"batch","models":null,
	 "limits":[{"type":"requests_per_minute","value":7,"org_limit":50,"source":{"type":"workspace"}}]},
	` + `{"type":"workspace_rate_limit","rate_limit_id":"rl_01SONNET","workspace_id":"wrkspc_01WS",
	 "group":{"id":"rlg_01SONNET","type":"model_group","display_name":"Claude Sonnet 4.x"},"group_type":"model_group","models":["claude-sonnet-4-5"],
	 "limits":[{"type":"requests_per_minute","value":150,"org_limit":4000,"source":{"type":"workspace"}},
	           {"type":"input_tokens_per_minute","value":999,"org_limit":400000,"source":{"type":"workspace"}}]}
],"next_page":null}`

func runRateLimitRead(t *testing.T, list string, status int, state WorkspaceRateLimitResourceModel) (resource.ReadResponse, []capturedRequest) {
	t.Helper()
	srv, requests := rateLimitServer(t, "", list, status)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}
	resp := resource.ReadResponse{State: rateLimitState(t, r, state)}
	r.Read(context.Background(), resource.ReadRequest{State: rateLimitState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	return resp, *requests
}

func TestWorkspaceRateLimitResource_ReadKeepsOnlyManagedTypes(t *testing.T) {
	resp, requests := runRateLimitRead(t, rateLimitsListForRead, http.StatusOK,
		rateLimitModel(map[string]int64{"requests_per_minute": 100, "tokens_per_day": 5}))

	if len(requests) != 1 || requests[0].method != http.MethodGet || requests[0].path != testRateLimitsListPath {
		t.Fatalf("requests = %+v, want one GET %s", requests, testRateLimitsListPath)
	}
	if _, ok := requests[0].query["include_inherited"]; ok {
		t.Errorf("Read must list overrides only, got include_inherited=%v", requests[0].query["include_inherited"])
	}

	limits, isNull := stateLimits(t, resp.State)
	if isNull {
		t.Fatal("limits is null")
	}
	if len(limits) != 1 || limits["requests_per_minute"] != 150 {
		t.Errorf("limits = %v, want only the managed requests_per_minute = 150", limits)
	}

	var got WorkspaceRateLimitResourceModel
	resp.State.Get(context.Background(), &got)
	if id := got.Group.Attributes()["id"].(types.String).ValueString(); id != "rlg_01SONNET" {
		t.Errorf("group.id = %q, want rlg_01SONNET", id)
	}
}

func TestWorkspaceRateLimitResource_ReadImportTakesEveryOverride(t *testing.T) {
	resp, _ := runRateLimitRead(t, rateLimitsListForRead, http.StatusOK, rateLimitModel(nil))

	limits, isNull := stateLimits(t, resp.State)
	if isNull {
		t.Fatal("limits is null after an import read")
	}
	if len(limits) != 2 || limits["requests_per_minute"] != 150 || limits["input_tokens_per_minute"] != 999 {
		t.Errorf("limits = %v, want every override of the group", limits)
	}
}

func TestWorkspaceRateLimitResource_ReadImportWithoutOverridesRemoves(t *testing.T) {
	resp, _ := runRateLimitRead(t, `{"data":[],"next_page":null}`, http.StatusOK, rateLimitModel(nil))
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed when there is nothing to import")
	}
}

func TestWorkspaceRateLimitResource_ReadClearedOutOfBandKeepsResource(t *testing.T) {
	resp, _ := runRateLimitRead(t, `{"data":[],"next_page":null}`, http.StatusOK,
		rateLimitModel(map[string]int64{"requests_per_minute": 100}))
	if resp.State.Raw.IsNull() {
		t.Fatal("resource removed; an override cleared out of band must show as drift on limits")
	}
	limits, isNull := stateLimits(t, resp.State)
	if isNull || len(limits) != 0 {
		t.Errorf("limits = %v (null %v), want an empty map", limits, isNull)
	}
}

func TestWorkspaceRateLimitResource_ReadWorkspaceNotFoundRemoves(t *testing.T) {
	resp, _ := runRateLimitRead(t, "", http.StatusNotFound, rateLimitModel(map[string]int64{"requests_per_minute": 100}))
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed after a 404")
	}
}

// --- ImportState ---

func TestWorkspaceRateLimitResource_ImportState(t *testing.T) {
	r := &WorkspaceRateLimitResource{}

	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "wrkspc_01WS:rl_01SONNET"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var got WorkspaceRateLimitResourceModel
	resp.State.Get(context.Background(), &got)
	if got.WorkspaceID.ValueString() != "wrkspc_01WS" || got.RateLimitID.ValueString() != "rl_01SONNET" {
		t.Errorf("workspace_id/rate_limit_id = %q/%q", got.WorkspaceID.ValueString(), got.RateLimitID.ValueString())
	}
	if !got.Limits.IsNull() {
		t.Errorf("limits = %v, want null so Read takes every override", got.Limits)
	}

	for _, bad := range []string{"wrkspc_01WS", ":rl_01SONNET", "wrkspc_01WS:", ""} {
		resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: bad}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("import ID %q: expected an error", bad)
		}
	}
}

// --- Schema validators ---

func TestWorkspaceRateLimitResource_limitsValidators(t *testing.T) {
	s := schematest.ResourceSchema(t, &WorkspaceRateLimitResource{})
	limitsAttr, ok := s.Attributes["limits"].(interface{ MapValidators() []validator.Map })
	if !ok {
		t.Fatal("limits has no map validators")
	}

	cases := map[string]struct {
		value   map[string]int64
		wantErr bool
	}{
		"valid":            {map[string]int64{"requests_per_minute": 100, "tool_uses_per_second": 0}, false},
		"unknown type":     {map[string]int64{"requests_per_hour": 100}, true},
		"negative":         {map[string]int64{"requests_per_minute": -1}, true},
		"above 2^53 - 1":   {map[string]int64{"requests_per_minute": maxWorkspaceRateLimitValue + 1}, true},
		"empty map":        {map[string]int64{}, true},
		"max value is set": {map[string]int64{"tokens_per_day": maxWorkspaceRateLimitValue}, false},
	}
	for name, tc := range cases {
		elems := map[string]attr.Value{}
		for k, v := range tc.value {
			elems[k] = types.Int64Value(v)
		}
		var resp validator.MapResponse
		for _, v := range limitsAttr.MapValidators() {
			v.ValidateMap(context.Background(), validator.MapRequest{ConfigValue: types.MapValueMust(types.Int64Type, elems)}, &resp)
		}
		if got := resp.Diagnostics.HasError(); got != tc.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", name, got, tc.wantErr, resp.Diagnostics)
		}
	}
}

// --- Helpers ---

func TestBuildWorkspaceRateLimitPatch_marshalsClearsAsNull(t *testing.T) {
	patch := buildWorkspaceRateLimitPatch(map[string]int64{"tokens_per_day": 0}, map[string]int64{"fast_otpm": 3, "tokens_per_day": 9})
	body, err := json.Marshal(workspaceRateLimitSetRequest{Limits: patch})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"limits":[{"type":"fast_otpm","value":null},{"type":"tokens_per_day","value":0}]}`
	if string(body) != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}
