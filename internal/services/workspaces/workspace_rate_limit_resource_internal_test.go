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
	"strings"
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

func i64(v int64) *int64 { return &v }

// newPrivateState returns an empty private state of the framework's type,
// which this package cannot name (it is internal to the framework), as the
// framework hands one to every Create, Read, Update and Delete. The argument
// only fixes the type.
func newPrivateState[T any](*T) *T { return new(T) }

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
	resp.Private = newPrivateState(resp.Private)
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
		{"output_tokens_per_minute", i64(20000)},
		{"requests_per_minute", i64(100)},
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
	resp.Private = newPrivateState(resp.Private)
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
		{"input_tokens_per_minute", i64(5000)},
		{"output_tokens_per_minute", nil},
		{"requests_per_minute", i64(200)},
	})

	limits, _ := stateLimits(t, resp.State)
	if len(limits) != 2 || limits["requests_per_minute"] != 200 || limits["input_tokens_per_minute"] != 5000 {
		t.Errorf("limits = %v, want the planned values", limits)
	}
}

// runRateLimitGroupUpdate runs an Update whose plan holds plannedGroup,
// against a server answering postResponse, and returns the stored group.
func runRateLimitGroupUpdate(t *testing.T, postResponse string, stateGroup, plannedGroup types.Object) types.Object {
	t.Helper()
	srv, _ := rateLimitServer(t, postResponse, "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	state := rateLimitModel(map[string]int64{"requests_per_minute": 100})
	state.Group = stateGroup
	plan := rateLimitModel(map[string]int64{"requests_per_minute": 200})
	plan.Group = plannedGroup

	resp := resource.UpdateResponse{State: rateLimitState(t, r, state)}
	resp.Private = newPrivateState(resp.Private)
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  rateLimitPlan(t, r, plan),
		State: rateLimitState(t, r, state),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	var got WorkspaceRateLimitResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return got.Group
}

func TestWorkspaceRateLimitResource_UpdateKeepsPlannedGroup(t *testing.T) {
	// The response's display_name differs from the planned one (renamed since
	// the last refresh); Terraform rejects a known planned value that changes.
	planned := types.ObjectValueMust(rateLimitGroupAttrTypes, map[string]attr.Value{
		"id":           types.StringValue("rlg_01SONNET"),
		"type":         types.StringValue("model_group"),
		"display_name": types.StringValue("Claude Sonnet"),
	})
	got := runRateLimitGroupUpdate(t, rateLimitEntryJSON(testRateLimitID,
		`[{"type":"requests_per_minute","value":200,"org_limit":4000,"source":{"type":"workspace"}}]`), planned, planned)
	if !got.Equal(planned) {
		t.Errorf("group = %v, want the planned %v", got, planned)
	}
}

func TestWorkspaceRateLimitResource_UpdateFillsUnknownGroup(t *testing.T) {
	cases := map[string]struct {
		response string
		wantNull bool
	}{
		"from the response": {rateLimitEntryJSON(testRateLimitID, `[]`), false},
		"null when the response has none": {`{"type":"workspace_rate_limit","rate_limit_id":"rl_01SONNET",` +
			`"workspace_id":"wrkspc_01WS","limits":[]}`, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := runRateLimitGroupUpdate(t, tc.response, types.ObjectNull(rateLimitGroupAttrTypes), types.ObjectUnknown(rateLimitGroupAttrTypes))
			if got.IsUnknown() {
				t.Fatal("group is still unknown after apply")
			}
			if got.IsNull() != tc.wantNull {
				t.Fatalf("group = %v, want null %v", got, tc.wantNull)
			}
			if !tc.wantNull {
				if id := got.Attributes()["id"].(types.String).ValueString(); id != "rlg_01SONNET" {
					t.Errorf("group.id = %q, want rlg_01SONNET", id)
				}
			}
		})
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

// A refresh whose list does not show the overrides Create just set yet drops
// them from state; Delete must still clear them, from the types Create
// recorded in private state, after a list that still lags.
func TestWorkspaceRateLimitResource_DeleteClearsTypesALaggingReadDropped(t *testing.T) {
	srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID,
		`[{"type":"output_tokens_per_minute","value":20000,"org_limit":80000,"source":{"type":"workspace"}},
		  {"type":"requests_per_minute","value":100,"org_limit":4000,"source":{"type":"workspace"}}]`),
		`{"data":[],"next_page":null}`, http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	private := createAndLaggingRead(t, r, map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 20000}).Private

	refreshed := rateLimitModel(map[string]int64{})
	resp := resource.DeleteResponse{State: rateLimitState(t, r, refreshed)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, refreshed), Private: private}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}

	if len(*requests) != 4 {
		t.Fatalf("requests = %d, want Create, Read, and Delete's list and POST", len(*requests))
	}
	if list := (*requests)[2]; list.method != http.MethodGet || list.path != testRateLimitsListPath {
		t.Fatalf("Delete's first request = %s %s, want GET %s", list.method, list.path, testRateLimitsListPath)
	}
	del := (*requests)[3]
	if del.method != http.MethodPost || del.path != testRateLimitPath {
		t.Fatalf("Delete request = %s %s, want POST %s", del.method, del.path, testRateLimitPath)
	}
	assertLimitsBody(t, del.limits, []struct {
		typ   string
		value *int64
	}{
		{"output_tokens_per_minute", nil},
		{"requests_per_minute", nil},
	})
}

// Same lag before an update that drops a type: Update must clear it, then
// record only the planned types, so a later Delete no longer clears it.
func TestWorkspaceRateLimitResource_UpdateClearsTypesALaggingReadDropped(t *testing.T) {
	srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID,
		`[{"type":"requests_per_minute","value":200,"org_limit":4000,"source":{"type":"workspace"}}]`),
		`{"data":[],"next_page":null}`, http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	private := createAndLaggingRead(t, r, map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 20000}).Private

	refreshed := rateLimitModel(map[string]int64{})
	plan := rateLimitModel(map[string]int64{"requests_per_minute": 200})
	updateResp := resource.UpdateResponse{State: rateLimitState(t, r, refreshed), Private: private}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:    rateLimitPlan(t, r, plan),
		State:   rateLimitState(t, r, refreshed),
		Private: private,
	}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", updateResp.Diagnostics)
	}
	assertLimitsBody(t, (*requests)[3].limits, []struct {
		typ   string
		value *int64
	}{
		{"output_tokens_per_minute", nil},
		{"requests_per_minute", i64(200)},
	})

	deleteResp := resource.DeleteResponse{State: rateLimitState(t, r, refreshed)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, refreshed), Private: updateResp.Private}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", deleteResp.Diagnostics)
	}
	assertLimitsBody(t, (*requests)[5].limits, []struct {
		typ   string
		value *int64
	}{
		{"requests_per_minute", nil},
	})
}

// withRecord records the overrides in private, as Create or Update would
// after setting them, and returns private.
func withRecord[P privateStateWriter](t *testing.T, private P, recorded map[string]int64) P {
	t.Helper()
	if diags := recordSetTypes(context.Background(), private, recorded); diags.HasError() {
		t.Fatalf("recordSetTypes: %v", diags)
	}
	return private
}

// A type the resource set, then cleared out of band (a read dropped it from
// state) and removed from the configuration, stays recorded: no update ran.
// Update and Delete clear it only while the group does not hold another
// value for it; set again in the Console or by another resource, it is left
// alone.
func TestWorkspaceRateLimitResource_RecordedTypeSetAgainElsewhere(t *testing.T) {
	recorded := map[string]int64{"output_tokens_per_minute": 20000, "requests_per_minute": 100}
	refreshed := rateLimitModel(map[string]int64{"output_tokens_per_minute": 20000})
	listWith := func(rpm string) string {
		return `{"data":[` + rateLimitEntryJSON(testRateLimitID,
			`[{"type":"output_tokens_per_minute","value":20000,"org_limit":80000,"source":{"type":"workspace"}}`+rpm+`]`) +
			`],"next_page":null}`
	}
	type limit = struct {
		typ   string
		value *int64
	}
	for _, tc := range []struct {
		name, list       string
		update, deletion []limit
	}{
		{
			name:     "set again with another value",
			list:     listWith(`,{"type":"requests_per_minute","value":500,"org_limit":4000,"source":{"type":"workspace"}}`),
			update:   []limit{{"output_tokens_per_minute", i64(30000)}},
			deletion: []limit{{"output_tokens_per_minute", nil}},
		},
		{
			name:     "the recorded value",
			list:     listWith(`,{"type":"requests_per_minute","value":100,"org_limit":4000,"source":{"type":"workspace"}}`),
			update:   []limit{{"output_tokens_per_minute", i64(30000)}, {"requests_per_minute", nil}},
			deletion: []limit{{"output_tokens_per_minute", nil}, {"requests_per_minute", nil}},
		},
		{
			name:     "no entry for the group",
			list:     `{"data":[],"next_page":null}`,
			update:   []limit{{"output_tokens_per_minute", i64(30000)}, {"requests_per_minute", nil}},
			deletion: []limit{{"output_tokens_per_minute", nil}, {"requests_per_minute", nil}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, requests := rateLimitServer(t, rateLimitEntryJSON(testRateLimitID, `[]`), tc.list, http.StatusOK)
			r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

			updateResp := resource.UpdateResponse{State: rateLimitState(t, r, refreshed)}
			updateResp.Private = newPrivateState(updateResp.Private)
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:    rateLimitPlan(t, r, rateLimitModel(map[string]int64{"output_tokens_per_minute": 30000})),
				State:   rateLimitState(t, r, refreshed),
				Private: withRecord(t, newPrivateState(resource.UpdateRequest{}.Private), recorded),
			}, &updateResp)
			if updateResp.Diagnostics.HasError() {
				t.Fatalf("Update: %v", updateResp.Diagnostics)
			}

			deleteResp := resource.DeleteResponse{State: rateLimitState(t, r, refreshed)}
			r.Delete(context.Background(), resource.DeleteRequest{
				State:   rateLimitState(t, r, refreshed),
				Private: withRecord(t, newPrivateState(resource.UpdateRequest{}.Private), recorded),
			}, &deleteResp)
			if deleteResp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", deleteResp.Diagnostics)
			}

			if len(*requests) != 4 {
				t.Fatalf("requests = %d, want a list and a POST for Update, then for Delete", len(*requests))
			}
			for _, i := range []int{0, 2} {
				if got := (*requests)[i]; got.method != http.MethodGet || got.path != testRateLimitsListPath {
					t.Errorf("request %d = %s %s, want GET %s", i, got.method, got.path, testRateLimitsListPath)
				}
			}
			assertLimitsBody(t, (*requests)[1].limits, tc.update)
			assertLimitsBody(t, (*requests)[3].limits, tc.deletion)
		})
	}
}

// The list that checks a recorded type is a read like Read's: a gone
// workspace (404) owns the type, and the request that follows gets the 404,
// which Delete tolerates; any other failure fails the operation before any
// override changes.
func TestWorkspaceRateLimitResource_RecordedTypeListFails(t *testing.T) {
	recorded := map[string]int64{"requests_per_minute": 100}
	refreshed := rateLimitModel(map[string]int64{})

	srv, requests := rateLimitServer(t, "", "", http.StatusNotFound)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}
	resp := resource.DeleteResponse{State: rateLimitState(t, r, refreshed)}
	r.Delete(context.Background(), resource.DeleteRequest{
		State:   rateLimitState(t, r, refreshed),
		Private: withRecord(t, newPrivateState(resource.UpdateRequest{}.Private), recorded),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete on a gone workspace must succeed, got: %v", resp.Diagnostics)
	}
	if len(*requests) != 2 || (*requests)[1].method != http.MethodPost {
		t.Fatalf("requests = %+v, want the list then the POST", *requests)
	}

	srv, requests = rateLimitServer(t, "", "", http.StatusInternalServerError)
	r = &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}
	resp = resource.DeleteResponse{State: rateLimitState(t, r, refreshed)}
	r.Delete(context.Background(), resource.DeleteRequest{
		State:   rateLimitState(t, r, refreshed),
		Private: withRecord(t, newPrivateState(resource.UpdateRequest{}.Private), recorded),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete succeeded although the list failed")
	}
	if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
		t.Errorf("requests = %+v, want only the failed list", *requests)
	}
}

// createAndLaggingRead creates the resource with limits, then refreshes it
// against a list that does not show the write yet, as the framework does:
// the private state Create returned goes through Read unchanged. It checks
// that the refresh dropped every type and returns the Read response.
func createAndLaggingRead(t *testing.T, r *WorkspaceRateLimitResource, limits map[string]int64) resource.ReadResponse {
	t.Helper()
	plan := rateLimitModel(limits)
	plan.ID = types.StringUnknown()
	plan.Group = types.ObjectUnknown(rateLimitGroupAttrTypes)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	createResp.Private = newPrivateState(createResp.Private)
	r.Create(context.Background(), resource.CreateRequest{Plan: rateLimitPlan(t, r, plan)}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", createResp.Diagnostics)
	}

	readResp := resource.ReadResponse{State: createResp.State, Private: createResp.Private}
	r.Read(context.Background(), resource.ReadRequest{State: createResp.State, Private: createResp.Private}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", readResp.Diagnostics)
	}
	if got, _ := stateLimits(t, readResp.State); len(got) != 0 {
		t.Fatalf("limits after the lagging read = %v, want none", got)
	}
	return readResp
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

// An override of a type outside the ones limits accepts (here
// enqueued_batch_requests, which the list can carry) is left out on import:
// a configuration could not declare it, and every later apply would clear it.
func TestWorkspaceRateLimitResource_ReadImportSkipsUndeclarableTypes(t *testing.T) {
	list := `{"data":[` + rateLimitEntryJSON(testRateLimitID,
		`[{"type":"requests_per_minute","value":150,"org_limit":4000,"source":{"type":"workspace"}},
		  {"type":"enqueued_batch_requests","value":20,"org_limit":500000,"source":{"type":"workspace"}}]`) +
		`],"next_page":null}`
	resp, _ := runRateLimitRead(t, list, http.StatusOK, rateLimitModel(nil))

	limits, isNull := stateLimits(t, resp.State)
	if isNull {
		t.Fatal("limits is null after an import read")
	}
	if len(limits) != 1 || limits["requests_per_minute"] != 150 {
		t.Errorf("limits = %v, want only requests_per_minute = 150", limits)
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

// A third segment names the types to import: ImportState records them in
// private state and keeps the two-segment id.
func TestWorkspaceRateLimitResource_ImportStateWithTypes(t *testing.T) {
	r := &WorkspaceRateLimitResource{}

	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	resp.Private = newPrivateState(resp.Private)
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "wrkspc_01WS:rl_01SONNET:requests_per_minute,tokens_per_day"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var got WorkspaceRateLimitResourceModel
	resp.State.Get(context.Background(), &got)
	if got.ID.ValueString() != "wrkspc_01WS:rl_01SONNET" || got.RateLimitID.ValueString() != "rl_01SONNET" {
		t.Errorf("id/rate_limit_id = %q/%q, want the two-segment id", got.ID.ValueString(), got.RateLimitID.ValueString())
	}
	names, diags := readImportTypes(context.Background(), resp.Private)
	if diags.HasError() || strings.Join(names, ",") != "requests_per_minute,tokens_per_day" {
		t.Errorf("recorded types = %v (%v), want [requests_per_minute tokens_per_day]", names, diags)
	}

	for _, bad := range []string{
		"wrkspc_01WS:rl_01SONNET:",                                        // empty list
		"wrkspc_01WS:rl_01SONNET:requests_per_hour",                       // not a type limits accepts
		"wrkspc_01WS:rl_01SONNET:requests_per_minute,",                    // empty entry
		"wrkspc_01WS:rl_01SONNET:requests_per_minute,requests_per_minute", // named twice
		"wrkspc_01WS:rl_01SONNET:enqueued_batch_requests",                 // listed by the API, not declarable
	} {
		resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
		resp.Private = newPrivateState(resp.Private)
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: bad}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("import ID %q: expected an error", bad)
		}
	}
}

// runRateLimitImportRead runs the Read that follows an import whose ID named
// the given types.
func runRateLimitImportRead(t *testing.T, list string, named []string) resource.ReadResponse {
	t.Helper()
	srv, _ := rateLimitServer(t, "", list, http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}

	importResp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	importResp.Private = newPrivateState(importResp.Private)
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "wrkspc_01WS:" + testRateLimitID + ":" + strings.Join(named, ",")}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", importResp.Diagnostics)
	}
	state := rateLimitModel(nil)
	resp := resource.ReadResponse{State: rateLimitState(t, r, state), Private: importResp.Private}
	r.Read(context.Background(), resource.ReadRequest{State: rateLimitState(t, r, state), Private: importResp.Private}, &resp)
	return resp
}

// The group overrides requests_per_minute and input_tokens_per_minute; an
// import ID naming only requests_per_minute leaves the other type to the
// resource that manages it, then clears the private-state entry.
func TestWorkspaceRateLimitResource_ReadImportTakesOnlyNamedTypes(t *testing.T) {
	resp := runRateLimitImportRead(t, rateLimitsListForRead, []string{"requests_per_minute"})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	limits, isNull := stateLimits(t, resp.State)
	if isNull || len(limits) != 1 || limits["requests_per_minute"] != 150 {
		t.Errorf("limits = %v (null %v), want only requests_per_minute = 150", limits, isNull)
	}
	if names, _ := readImportTypes(context.Background(), resp.Private); names != nil {
		t.Errorf("import types still recorded after Read: %v", names)
	}
}

// A named type the group does not override fails the import, rather than
// importing a resource that would set it on the next apply.
func TestWorkspaceRateLimitResource_ReadImportNamedTypeWithoutOverrideFails(t *testing.T) {
	resp := runRateLimitImportRead(t, rateLimitsListForRead, []string{"requests_per_minute", "tokens_per_day"})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for a named type with no override")
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "tokens_per_day") || strings.Contains(detail, "requests_per_minute") {
		t.Errorf("detail = %q, want it to name tokens_per_day only", detail)
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
		value   map[string]attr.Value
		wantErr bool
	}{
		"valid":            {int64Elems(map[string]int64{"requests_per_minute": 100, "tool_uses_per_second": 0}), false},
		"unknown type":     {int64Elems(map[string]int64{"requests_per_hour": 100}), true},
		"negative":         {int64Elems(map[string]int64{"requests_per_minute": -1}), true},
		"above 2^53 - 1":   {int64Elems(map[string]int64{"requests_per_minute": maxWorkspaceRateLimitValue + 1}), true},
		"empty map":        {int64Elems(map[string]int64{}), true},
		"max value is set": {int64Elems(map[string]int64{"tokens_per_day": maxWorkspaceRateLimitValue}), false},
		// A null value (e.g. a variable defaulting to null) would otherwise
		// pass plan and fail at apply when the map is read into int64s.
		"null value": {map[string]attr.Value{"requests_per_minute": types.Int64Null(), "tokens_per_day": types.Int64Value(5)}, true},
		// An unknown value is checked again once known, at apply.
		"unknown value": {map[string]attr.Value{"requests_per_minute": types.Int64Unknown()}, false},
	}
	for name, tc := range cases {
		var resp validator.MapResponse
		for _, v := range limitsAttr.MapValidators() {
			v.ValidateMap(context.Background(), validator.MapRequest{ConfigValue: types.MapValueMust(types.Int64Type, tc.value)}, &resp)
		}
		if got := resp.Diagnostics.HasError(); got != tc.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", name, got, tc.wantErr, resp.Diagnostics)
		}
	}
}

// --- Helpers ---

func int64Elems(m map[string]int64) map[string]attr.Value {
	elems := map[string]attr.Value{}
	for k, v := range m {
		elems[k] = types.Int64Value(v)
	}
	return elems
}

func TestBuildWorkspaceRateLimitPatch_marshalsClearsAsNull(t *testing.T) {
	patch := buildWorkspaceRateLimitPatch(map[string]int64{"tokens_per_day": 0}, map[string]bool{"fast_otpm": true, "tokens_per_day": true})
	body, err := json.Marshal(workspaceRateLimitSetRequest{Limits: patch})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"limits":[{"type":"fast_otpm","value":null},{"type":"tokens_per_day","value":0}]}`
	if string(body) != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

// A record that is not an object of type to value fails Delete before any
// request: which types the resource set is unknown.
func TestWorkspaceRateLimitResource_InvalidRecordFails(t *testing.T) {
	srv, requests := rateLimitServer(t, "", "", http.StatusOK)
	r := &WorkspaceRateLimitResource{adminClient: newTestAdminClient(t, srv)}
	private := newPrivateState(resource.DeleteRequest{}.Private)
	if diags := private.SetKey(context.Background(), setTypesPrivateKey, []byte(`["requests_per_minute"]`)); diags.HasError() {
		t.Fatalf("SetKey: %v", diags)
	}

	state := rateLimitModel(map[string]int64{"output_tokens_per_minute": 20000})
	resp := resource.DeleteResponse{State: rateLimitState(t, r, state)}
	r.Delete(context.Background(), resource.DeleteRequest{State: rateLimitState(t, r, state), Private: private}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete succeeded with an unreadable record")
	}
	if len(*requests) != 0 {
		t.Errorf("requests = %d, want 0", len(*requests))
	}
}
