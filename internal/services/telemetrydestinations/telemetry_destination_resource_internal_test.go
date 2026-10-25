// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live CRUD acceptance tests for anthropic_telemetry_destination are not
// written: Admin API resource tests wait for a dedicated test organization
// (#58, see CLAUDE.md), and a destination exports the organization's real
// traces. Coverage here is httptest-based unit tests only.
package telemetrydestinations

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const testHeaderValue = "Bearer super-secret-collector-token"

func workspacesObject(t *testing.T, typ string, ids ...string) types.Object {
	t.Helper()
	set := types.SetNull(types.StringType)
	if ids != nil {
		set = types.SetValueMust(types.StringType, stringValues(ids))
	}
	return types.ObjectValueMust(workspacesAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(typ),
		"workspace_ids": set,
	})
}

func tracesObject(contentMode string, endpointURL types.String, maxBytes types.Int64) types.Object {
	return types.ObjectValueMust(tracesAttrTypes, map[string]attr.Value{
		"content_mode":      types.StringValue(contentMode),
		"enabled":           types.BoolValue(true),
		"endpoint_url":      endpointURL,
		"max_content_bytes": maxBytes,
	})
}

func stringValues(in []string) []attr.Value {
	out := make([]attr.Value, len(in))
	for i, s := range in {
		out[i] = types.StringValue(s)
	}
	return out
}

func headersMap(kv map[string]string) types.Map {
	elems := make(map[string]attr.Value, len(kv))
	for k, v := range kv {
		elems[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, elems)
}

// plannedDestination is the resource as planned on create.
func plannedDestination(t *testing.T) TelemetryDestinationResourceModel {
	t.Helper()
	return TelemetryDestinationResourceModel{
		ID:               types.StringUnknown(),
		DisplayName:      types.StringValue("Collector"),
		EndpointURL:      types.StringValue("https://gateway.example.com/otlp"),
		Enabled:          types.BoolValue(true),
		DisabledReason:   types.StringUnknown(),
		Workspaces:       workspacesObject(t, "include", "wrkspc_b", "wrkspc_a"),
		Traces:           tracesObject("metadata_only", types.StringNull(), types.Int64Unknown()),
		HeadersWO:        types.MapNull(types.StringType),
		HeadersWOVersion: types.Int64Value(1),
		HeaderNames:      types.SetUnknown(types.StringType),
		DeliveryHealth:   types.ObjectUnknown(deliveryHealthAttrTypes),
		CreatedAt:        types.StringUnknown(),
		UpdatedAt:        types.StringUnknown(),
	}
}

// storedDestination is the fixture destination as stored after a create.
func storedDestination(t *testing.T) TelemetryDestinationResourceModel {
	t.Helper()
	m := plannedDestination(t)
	m.ID = types.StringValue(testDestinationID)
	m.DisabledReason = types.StringNull()
	m.Traces = tracesObject("metadata_only", types.StringNull(), types.Int64Null())
	m.HeaderNames = types.SetValueMust(types.StringType, stringValues([]string{"authorization"}))
	m.DeliveryHealth = types.ObjectValueMust(deliveryHealthAttrTypes, map[string]attr.Value{
		"last_error":      types.StringNull(),
		"last_error_at":   types.StringNull(),
		"last_success_at": types.StringValue("2026-10-20T19:05:00Z"),
	})
	m.CreatedAt = types.StringValue("2026-10-20T19:00:00Z")
	m.UpdatedAt = types.StringValue("2026-10-20T19:00:00Z")
	return m
}

// updatePlan derives an update plan from state: the attributes without
// UseStateForUnknown are unknown, as Terraform plans them.
func updatePlan(state TelemetryDestinationResourceModel) TelemetryDestinationResourceModel {
	p := state
	p.DisabledReason = types.StringUnknown()
	p.DeliveryHealth = types.ObjectUnknown(deliveryHealthAttrTypes)
	p.UpdatedAt = types.StringUnknown()
	return p
}

func toPlan(t *testing.T, r resource.Resource, m TelemetryDestinationResourceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: schematest.ResourceSchema(t, r)}
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("plan: %v", diags)
	}
	return p
}

func toState(t *testing.T, r resource.Resource, m TelemetryDestinationResourceModel) tfsdk.State {
	t.Helper()
	s := tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
	if diags := s.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return s
}

// toConfig builds a configuration from a model: unlike the plan, it carries
// the write-only headers_wo.
func toConfig(t *testing.T, r resource.Resource, m TelemetryDestinationResourceModel, headers types.Map) tfsdk.Config {
	t.Helper()
	m.HeadersWO = headers
	p := toPlan(t, r, m)
	return tfsdk.Config(p)
}

func fromState(t *testing.T, s tfsdk.State) TelemetryDestinationResourceModel {
	t.Helper()
	var m TelemetryDestinationResourceModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	return m
}

func workspaceIDsOf(t *testing.T, obj types.Object) []string {
	t.Helper()
	var ws workspacesModel
	if diags := obj.As(context.Background(), &ws, objectAsOptions); diags.HasError() {
		t.Fatalf("workspaces: %v", diags)
	}
	if ws.WorkspaceIDs.IsNull() {
		return nil
	}
	var ids []string
	ws.WorkspaceIDs.ElementsAs(context.Background(), &ids, false)
	return ids
}

// assertNoHeaderValue fails if the write-only value reached the state in any
// form.
func assertNoHeaderValue(t *testing.T, s tfsdk.State) {
	t.Helper()
	if strings.Contains(s.Raw.String(), testHeaderValue) {
		t.Errorf("the write-only header value reached the state: %s", s.Raw.String())
	}
	if m := fromState(t, s); !m.HeadersWO.IsNull() {
		t.Errorf("headers_wo = %v, want null in state", m.HeadersWO)
	}
}

// --- Create ---

func TestTelemetryDestinationResource_Create(t *testing.T) {
	t.Parallel()

	api, client := newFakeTelemetryAPI(t, func(recordedRequest) (int, string) {
		return http.StatusOK, destinationJSON(t, nil)
	})
	r := &TelemetryDestinationResource{client: client}

	plan := plannedDestination(t)
	cfg := toConfig(t, r, plan, headersMap(map[string]string{"authorization": testHeaderValue}))
	resp := resource.CreateResponse{State: tfsdk.State{Schema: cfg.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: toPlan(t, r, plan), Config: cfg}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if got, want := api.calls(), []string{"POST /v1/organizations/telemetry_destinations"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)

	body := api.request(t, 0).Body
	want := map[string]any{
		"endpoint_url": "https://gateway.example.com/otlp",
		"display_name": "Collector",
		"workspaces":   map[string]any{"type": "include", "workspace_ids": []any{"wrkspc_a", "wrkspc_b"}},
		"traces":       map[string]any{"content_mode": "metadata_only", "enabled": true},
		"headers":      map[string]any{"authorization": testHeaderValue},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("create body = %v\nwant %v", body, want)
	}

	got := fromState(t, resp.State)
	assertNoHeaderValue(t, resp.State)
	if got.ID.ValueString() != testDestinationID || got.HeadersWOVersion.ValueInt64() != 1 {
		t.Errorf("id/headers_wo_version = %v/%v", got.ID, got.HeadersWOVersion)
	}
	if ids := workspaceIDsOf(t, got.Workspaces); len(ids) != 2 {
		t.Errorf("workspace_ids = %v, want the planned list (the create response has the type only)", ids)
	}
	var names []string
	got.HeaderNames.ElementsAs(context.Background(), &names, false)
	if !reflect.DeepEqual(names, []string{"authorization"}) {
		t.Errorf("header_names = %v", names)
	}
}

func TestTelemetryDestinationResource_CreateDisabledSendsSecondCall(t *testing.T) {
	t.Parallel()

	api, client := newFakeTelemetryAPI(t, func(r recordedRequest) (int, string) {
		if r.Path == telemetryDestinationsPath {
			return http.StatusOK, destinationJSON(t, nil)
		}
		return http.StatusOK, destinationJSON(t, map[string]any{"state": map[string]any{"type": "disabled", "reason": map[string]any{"type": "caller"}}})
	})
	r := &TelemetryDestinationResource{client: client}

	plan := plannedDestination(t)
	plan.Enabled = types.BoolValue(false)
	cfg := toConfig(t, r, plan, types.MapNull(types.StringType))
	resp := resource.CreateResponse{State: tfsdk.State{Schema: cfg.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: toPlan(t, r, plan), Config: cfg}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	want := []string{"POST /v1/organizations/telemetry_destinations", "POST /v1/organizations/telemetry_destinations/" + testDestinationID}
	if got := api.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)
	if _, ok := api.request(t, 0).Body["headers"]; ok {
		t.Error("headers sent although headers_wo is not configured")
	}
	if body := api.request(t, 1).Body; !reflect.DeepEqual(body, map[string]any{"state": map[string]any{"type": "disabled"}}) {
		t.Errorf("disable body = %v", body)
	}
	got := fromState(t, resp.State)
	if got.Enabled.ValueBool() || got.DisabledReason.ValueString() != "caller" {
		t.Errorf("enabled/disabled_reason = %v/%v", got.Enabled, got.DisabledReason)
	}
}

// --- Read ---

func TestTelemetryDestinationResource_ReadListsWorkspaces(t *testing.T) {
	t.Parallel()

	next := "page_2"
	api, client := newFakeTelemetryAPI(t, func(r recordedRequest) (int, string) {
		switch {
		case strings.HasSuffix(r.Path, "/workspaces") && strings.Contains(r.Query, "page=page_2"):
			return http.StatusOK, workspacesPageJSON(t, nil, "wrkspc_c")
		case strings.HasSuffix(r.Path, "/workspaces"):
			return http.StatusOK, workspacesPageJSON(t, &next, "wrkspc_a", "wrkspc_b")
		}
		return http.StatusOK, destinationJSON(t, map[string]any{
			"display_name": nil,
			"header_names": []string{"authorization", "x-scope"},
			"state":        map[string]any{"type": "disabled", "reason": map[string]any{"type": "sustained_failure"}},
			"traces":       map[string]any{"content_mode": "content", "enabled": true, "endpoint_url": "https://gateway.example.com/custom/traces", "max_content_bytes": 786432},
		})
	})
	r := &TelemetryDestinationResource{client: client}

	prior := storedDestination(t)
	prior.HeadersWOVersion = types.Int64Value(4)
	resp := resource.ReadResponse{State: toState(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: toState(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	want := []string{
		"GET /v1/organizations/telemetry_destinations/" + testDestinationID,
		"GET /v1/organizations/telemetry_destinations/" + testDestinationID + "/workspaces",
		"GET /v1/organizations/telemetry_destinations/" + testDestinationID + "/workspaces",
	}
	if got := api.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)
	if q := api.request(t, 1).Query; q != "limit=1000" {
		t.Errorf("first workspaces page query = %q", q)
	}

	got := fromState(t, resp.State)
	assertNoHeaderValue(t, resp.State)
	if ids := workspaceIDsOf(t, got.Workspaces); len(ids) != 3 {
		t.Errorf("workspace_ids = %v, want both pages", ids)
	}
	if got.HeadersWOVersion.ValueInt64() != 4 {
		t.Errorf("headers_wo_version = %v, want it kept from state", got.HeadersWOVersion)
	}
	if !got.DisplayName.IsNull() || got.Enabled.ValueBool() || got.DisabledReason.ValueString() != "sustained_failure" {
		t.Errorf("display_name/enabled/disabled_reason = %v/%v/%v", got.DisplayName, got.Enabled, got.DisabledReason)
	}
	if len(got.HeaderNames.Elements()) != 2 {
		t.Errorf("header_names = %v", got.HeaderNames)
	}
	var tr tracesModel
	got.Traces.As(context.Background(), &tr, objectAsOptions)
	if tr.ContentMode.ValueString() != "content" || tr.MaxContentBytes.ValueInt64() != 786432 || tr.EndpointURL.ValueString() == "" {
		t.Errorf("traces = %+v", tr)
	}
}

func TestTelemetryDestinationResource_ReadTypeAllSkipsWorkspaceList(t *testing.T) {
	t.Parallel()

	api, client := newFakeTelemetryAPI(t, func(recordedRequest) (int, string) {
		return http.StatusOK, destinationJSON(t, map[string]any{"workspaces": map[string]any{"type": "all"}})
	})
	r := &TelemetryDestinationResource{client: client}

	prior := storedDestination(t)
	prior.Workspaces = workspacesObject(t, "all")
	resp := resource.ReadResponse{State: toState(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: toState(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if got := api.calls(); len(got) != 1 {
		t.Errorf("calls = %v, want only the destination read", got)
	}
	if ids := workspaceIDsOf(t, fromState(t, resp.State).Workspaces); ids != nil {
		t.Errorf("workspace_ids = %v, want null for type all", ids)
	}
}

func TestTelemetryDestinationResource_ReadNotFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	_, client := newFakeTelemetryAPI(t, func(recordedRequest) (int, string) { return notFound() })
	r := &TelemetryDestinationResource{client: client}

	state := toState(t, r, storedDestination(t))
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state after a 404")
	}
}

// --- Update ---

// runUpdate applies plan over state with the given configured headers and
// returns the fake API and the new state.
func runUpdate(t *testing.T, state, plan TelemetryDestinationResourceModel, headers types.Map, handle func(recordedRequest) (int, string)) (*fakeTelemetryAPI, resource.UpdateResponse) {
	t.Helper()
	api, client := newFakeTelemetryAPI(t, handle)
	r := &TelemetryDestinationResource{client: client}
	resp := resource.UpdateResponse{State: toState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:   toPlan(t, r, plan),
		State:  toState(t, r, state),
		Config: toConfig(t, r, plan, headers),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	api.assertBetaOnEveryRequest(t)
	return api, resp
}

func okDestination(t *testing.T) func(recordedRequest) (int, string) {
	return func(recordedRequest) (int, string) { return http.StatusOK, destinationJSON(t, nil) }
}

func TestTelemetryDestinationResource_UpdateVersionBumpResendsHeaders(t *testing.T) {
	t.Parallel()

	state := storedDestination(t)
	plan := updatePlan(state)
	plan.HeadersWOVersion = types.Int64Value(2)

	api, resp := runUpdate(t, state, plan, headersMap(map[string]string{"authorization": testHeaderValue}), okDestination(t))

	if got, want := api.calls(), []string{"POST /v1/organizations/telemetry_destinations/" + testDestinationID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	want := map[string]any{
		"headers":      map[string]any{"authorization": testHeaderValue},
		"endpoint_url": "https://gateway.example.com/otlp",
	}
	if body := api.request(t, 0).Body; !reflect.DeepEqual(body, want) {
		t.Errorf("update body = %v\nwant %v (a request that sets a header value must carry endpoint_url)", body, want)
	}
	assertNoHeaderValue(t, resp.State)
	if v := fromState(t, resp.State).HeadersWOVersion.ValueInt64(); v != 2 {
		t.Errorf("headers_wo_version = %d, want 2", v)
	}
}

func TestTelemetryDestinationResource_UpdateHostChangeResendsHeaders(t *testing.T) {
	t.Parallel()

	state := storedDestination(t)
	state.Traces = tracesObject("metadata_only", types.StringValue("https://gateway.example.com/custom/traces"), types.Int64Null())
	plan := updatePlan(state)
	plan.EndpointURL = types.StringValue("https://collector.example.org/otlp")
	plan.Traces = tracesObject("metadata_only", types.StringValue("https://collector.example.org/custom/traces"), types.Int64Null())
	plan.HeaderNames = types.SetUnknown(types.StringType)

	api, _ := runUpdate(t, state, plan, headersMap(map[string]string{"authorization": testHeaderValue}), okDestination(t))

	body := api.request(t, 0).Body
	if body["endpoint_url"] != "https://collector.example.org/otlp" {
		t.Errorf("endpoint_url = %v", body["endpoint_url"])
	}
	if !reflect.DeepEqual(body["headers"], map[string]any{"authorization": testHeaderValue}) {
		t.Errorf("headers = %v, want them re-sent: a host change removes every registered header", body["headers"])
	}
	traces, _ := body["traces"].(map[string]any)
	if traces["endpoint_url"] != "https://collector.example.org/custom/traces" {
		t.Errorf("traces = %v, want the new traces URL in the same request", body["traces"])
	}
}

func TestTelemetryDestinationResource_UpdateRemovedHeaderSentAsNull(t *testing.T) {
	t.Parallel()

	state := storedDestination(t)
	state.HeaderNames = types.SetValueMust(types.StringType, stringValues([]string{"Authorization", "x-scope"}))
	plan := updatePlan(state)
	plan.HeaderNames = types.SetUnknown(types.StringType)

	// The configuration spells the kept header in lower case; the patch keeps
	// the registered spelling so header_names does not change behind the plan.
	api, _ := runUpdate(t, state, plan, headersMap(map[string]string{"authorization": testHeaderValue}), okDestination(t))

	want := map[string]any{"Authorization": testHeaderValue, "x-scope": nil}
	if got := api.request(t, 0).Body["headers"]; !reflect.DeepEqual(got, want) {
		t.Errorf("headers = %v, want %v", got, want)
	}
}

func TestTelemetryDestinationResource_UpdateUnmanagedHeadersLeftAlone(t *testing.T) {
	t.Parallel()

	state := storedDestination(t)
	plan := updatePlan(state)
	plan.DisplayName = types.StringNull()
	plan.HeadersWOVersion = types.Int64Null()

	api, _ := runUpdate(t, state, plan, types.MapNull(types.StringType), okDestination(t))

	if body, want := api.request(t, 0).Body, map[string]any{"display_name": nil}; !reflect.DeepEqual(body, want) {
		t.Errorf("update body = %v, want only display_name cleared with an explicit null", body)
	}
}

func TestTelemetryDestinationResource_UpdateWorkspacesSentWhole(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		plan types.Object
		want map[string]any
	}{
		"exclude without ids empties the list": {workspacesObject(t, "exclude"), map[string]any{"type": "exclude", "workspace_ids": []any{}}},
		"include replaces the list":            {workspacesObject(t, "include", "wrkspc_c"), map[string]any{"type": "include", "workspace_ids": []any{"wrkspc_c"}}},
		"all has no list":                      {workspacesObject(t, "all"), map[string]any{"type": "all"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := storedDestination(t)
			plan := updatePlan(state)
			plan.Workspaces = tc.plan
			api, resp := runUpdate(t, state, plan, types.MapNull(types.StringType), func(recordedRequest) (int, string) {
				return http.StatusOK, destinationJSON(t, map[string]any{"workspaces": map[string]any{"type": tc.want["type"]}})
			})

			if body := api.request(t, 0).Body; !reflect.DeepEqual(body, map[string]any{"workspaces": tc.want}) {
				t.Errorf("update body = %v, want only workspaces = %v", body, tc.want)
			}
			// The update response carries the type only: the IDs in state come
			// from the plan, since the request replaced the whole list.
			if got, want := workspaceIDsOf(t, fromState(t, resp.State).Workspaces), workspaceIDsOf(t, tc.plan); !reflect.DeepEqual(got, want) {
				t.Errorf("workspace_ids = %v, want the planned %v", got, want)
			}
		})
	}
}

func TestTelemetryDestinationResource_UpdateNothingToSendReads(t *testing.T) {
	t.Parallel()

	state := storedDestination(t)
	state.HeaderNames = types.SetValueMust(types.StringType, []attr.Value{})
	plan := updatePlan(state)
	plan.HeadersWOVersion = types.Int64Value(2)

	api, _ := runUpdate(t, state, plan, headersMap(map[string]string{}), okDestination(t))

	if got, want := api.calls(), []string{"GET /v1/organizations/telemetry_destinations/" + testDestinationID}; !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want a read instead of an empty update the API would refuse", got)
	}
}

// --- Delete ---

func TestTelemetryDestinationResource_Delete(t *testing.T) {
	t.Parallel()

	for name, gone := range map[string]bool{"deleted": false, "already gone": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api, client := newFakeTelemetryAPI(t, func(recordedRequest) (int, string) {
				if gone {
					return notFound()
				}
				return http.StatusOK, `{"type":"telemetry_destination_deleted","id":"` + testDestinationID + `"}`
			})
			r := &TelemetryDestinationResource{client: client}
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: toState(t, r, storedDestination(t))}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			if got, want := api.calls(), []string{"DELETE /v1/organizations/telemetry_destinations/" + testDestinationID}; !reflect.DeepEqual(got, want) {
				t.Errorf("calls = %v, want %v", got, want)
			}
			api.assertBetaOnEveryRequest(t)
		})
	}
}

func TestTelemetryDestinationResource_ImportState(t *testing.T) {
	t.Parallel()

	r := &TelemetryDestinationResource{}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testDestinationID}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var id string
	resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if id != testDestinationID {
		t.Errorf("id = %q", id)
	}
}

// --- Validation and plan ---

func TestTelemetryDestinationConfigValidator(t *testing.T) {
	t.Parallel()

	r := &TelemetryDestinationResource{}
	cases := map[string]struct {
		mutate    func(m *TelemetryDestinationResourceModel)
		headers   types.Map
		wantError string
	}{
		"headers with a version": {
			headers: headersMap(map[string]string{"authorization": "x"}),
		},
		"headers without a version": {
			mutate:    func(m *TelemetryDestinationResourceModel) { m.HeadersWOVersion = types.Int64Null() },
			headers:   headersMap(map[string]string{"authorization": "x"}),
			wantError: "headers_wo_version",
		},
		"unknown version is not missing": {
			mutate:  func(m *TelemetryDestinationResourceModel) { m.HeadersWOVersion = types.Int64Unknown() },
			headers: headersMap(map[string]string{"authorization": "x"}),
		},
		"version without headers": {
			headers: types.MapNull(types.StringType),
		},
		"names differing only in case": {
			headers:   headersMap(map[string]string{"Authorization": "x", "authorization": "y"}),
			wantError: "Duplicate header name",
		},
		"workspace_ids with type all": {
			mutate:    func(m *TelemetryDestinationResourceModel) { m.Workspaces = workspacesObject(t, "all", "wrkspc_a") },
			headers:   types.MapNull(types.StringType),
			wantError: "must not be set",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := plannedDestination(t)
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			resp := resource.ValidateConfigResponse{}
			telemetryDestinationConfigValidator{}.ValidateResource(context.Background(),
				resource.ValidateConfigRequest{Config: toConfig(t, r, m, tc.headers)}, &resp)
			if tc.wantError == "" {
				if resp.Diagnostics.HasError() {
					t.Errorf("unexpected error: %v", resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Summary()+resp.Diagnostics.Errors()[0].Detail(), tc.wantError) {
				t.Errorf("diagnostics = %v, want an error mentioning %q", resp.Diagnostics, tc.wantError)
			}
		})
	}
}

func TestHeaderNamesPlanModifier(t *testing.T) {
	t.Parallel()

	r := &TelemetryDestinationResource{}
	registered := types.SetValueMust(types.StringType, stringValues([]string{"Authorization"}))
	cases := map[string]struct {
		headers     types.Map
		endpointURL string
		wantUnknown bool
		wantWarning bool
	}{
		"same names, other case":           {headersMap(map[string]string{"authorization": "x"}), "https://gateway.example.com/otlp", false, false},
		"added name":                       {headersMap(map[string]string{"authorization": "x", "x-scope": "y"}), "https://gateway.example.com/otlp", true, false},
		"removed name":                     {headersMap(map[string]string{}), "https://gateway.example.com/otlp", true, false},
		"path change keeps headers":        {headersMap(map[string]string{"authorization": "x"}), "https://gateway.example.com/v2/otlp", false, false},
		"host change re-sends headers":     {headersMap(map[string]string{"authorization": "x"}), "https://collector.example.org/otlp", true, false},
		"unmanaged headers":                {types.MapNull(types.StringType), "https://gateway.example.com/otlp", false, false},
		"unmanaged headers, host change":   {types.MapNull(types.StringType), "https://gateway.example.com:8443/otlp", true, true},
		"headers unknown until apply time": {types.MapUnknown(types.StringType), "https://gateway.example.com/otlp", true, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := storedDestination(t)
			state.HeaderNames = registered
			plan := updatePlan(state)
			plan.EndpointURL = types.StringValue(tc.endpointURL)
			plan.HeaderNames = types.SetUnknown(types.StringType)

			req := planmodifier.SetRequest{
				Path:       path.Root("header_names"),
				Config:     toConfig(t, r, plan, tc.headers),
				Plan:       toPlan(t, r, plan),
				State:      toState(t, r, state),
				PlanValue:  types.SetUnknown(types.StringType),
				StateValue: registered,
			}
			resp := planmodifier.SetResponse{PlanValue: req.PlanValue}
			headerNamesPlanModifier{}.PlanModifySet(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if got := resp.PlanValue.IsUnknown(); got != tc.wantUnknown {
				t.Errorf("unknown = %v, want %v (plan %v)", got, tc.wantUnknown, resp.PlanValue)
			}
			if !tc.wantUnknown && !resp.PlanValue.Equal(registered) {
				t.Errorf("plan = %v, want the state value", resp.PlanValue)
			}
			if got := resp.Diagnostics.WarningsCount() > 0; got != tc.wantWarning {
				t.Errorf("warning = %v, want %v", got, tc.wantWarning)
			}
		})
	}

	t.Run("create leaves the names unknown", func(t *testing.T) {
		t.Parallel()
		plan := plannedDestination(t)
		req := planmodifier.SetRequest{
			Config: toConfig(t, r, plan, types.MapNull(types.StringType)),
			Plan:   toPlan(t, r, plan),
			State: tfsdk.State{
				Schema: schematest.ResourceSchema(t, r),
				Raw:    tftypes.NewValue(schematest.ResourceObjectType(t, r), nil),
			},
			PlanValue: types.SetUnknown(types.StringType),
		}
		resp := planmodifier.SetResponse{PlanValue: req.PlanValue}
		headerNamesPlanModifier{}.PlanModifySet(context.Background(), req, &resp)
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("plan = %v, want unknown on create", resp.PlanValue)
		}
	})
}

// TestTracesDefaults pins the defaults of a configured traces block, and that
// an omitted block keeps its planned (state) value instead of getting them.
func TestTracesDefaults(t *testing.T) {
	t.Parallel()

	r := &TelemetryDestinationResource{}
	configured := plannedDestination(t)
	configured.Traces = types.ObjectValueMust(tracesAttrTypes, map[string]attr.Value{
		"content_mode":      types.StringNull(),
		"enabled":           types.BoolNull(),
		"endpoint_url":      types.StringNull(),
		"max_content_bytes": types.Int64Null(),
	})
	omitted := plannedDestination(t)
	omitted.Traces = types.ObjectNull(tracesAttrTypes)

	for name, tc := range map[string]struct {
		model       TelemetryDestinationResourceModel
		wantDefault bool
	}{
		"block set without the attributes": {configured, true},
		"block omitted":                    {omitted, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := toConfig(t, r, tc.model, types.MapNull(types.StringType))

			sReq := planmodifier.StringRequest{Config: cfg, ConfigValue: types.StringNull(), PlanValue: types.StringValue("content")}
			sResp := planmodifier.StringResponse{PlanValue: sReq.PlanValue}
			tracesDefaultString{value: contentModeMetadataOnly}.PlanModifyString(context.Background(), sReq, &sResp)

			bReq := planmodifier.BoolRequest{Config: cfg, ConfigValue: types.BoolNull(), PlanValue: types.BoolValue(false)}
			bResp := planmodifier.BoolResponse{PlanValue: bReq.PlanValue}
			tracesDefaultBool{value: true}.PlanModifyBool(context.Background(), bReq, &bResp)

			if tc.wantDefault {
				if sResp.PlanValue.ValueString() != contentModeMetadataOnly || !bResp.PlanValue.ValueBool() {
					t.Errorf("content_mode/enabled = %v/%v, want the defaults", sResp.PlanValue, bResp.PlanValue)
				}
				return
			}
			if sResp.PlanValue.ValueString() != "content" || bResp.PlanValue.ValueBool() {
				t.Errorf("content_mode/enabled = %v/%v, want the planned values kept", sResp.PlanValue, bResp.PlanValue)
			}
		})
	}
}

func TestEndpointHostOrPortChanged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		before, after string
		want          bool
	}{
		{"https://gateway.example.com/otlp", "https://gateway.example.com/otlp", false},
		{"https://gateway.example.com/otlp", "https://gateway.example.com/v2", false},
		{"https://gateway.example.com/otlp", "https://GATEWAY.example.com/otlp", false},
		{"https://gateway.example.com/otlp", "https://gateway.example.com:443/otlp", false},
		{"https://gateway.example.com/otlp", "https://collector.example.com/otlp", true},
		{"https://gateway.example.com/otlp", "https://gateway.example.com:8443/otlp", true},
		{"https://gateway.example.com/otlp", "https://[::1", true},
	}
	for _, tc := range cases {
		if got := endpointHostOrPortChanged(tc.before, tc.after); got != tc.want {
			t.Errorf("endpointHostOrPortChanged(%q, %q) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}
