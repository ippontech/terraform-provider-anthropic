// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live CRUD acceptance tests for anthropic_organization_webhook are not
// written: Admin API resource tests wait for a dedicated test organization
// (#58, see CLAUDE.md), and organization webhooks are a beta Anthropic turns on
// per organization. Coverage here is httptest-based unit tests only.
package organizationwebhooks

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const testWebhookID = "wep_011CZkZDf8HnUq2mTxRbVn4s"

// baseWebhookModel is the resource as planned for the fixture endpoint.
func baseWebhookModel(t *testing.T) OrganizationWebhookResourceModel {
	t.Helper()
	events, diags := types.SetValueFrom(context.Background(), types.StringType,
		[]string{"user_profile.enforcement_action_opened", "user_profile.enforcement_action_resolved"})
	if diags.HasError() {
		t.Fatalf("events: %v", diags)
	}
	return OrganizationWebhookResourceModel{
		ID:                   types.StringUnknown(),
		URL:                  types.StringValue("https://example.com/anthropic-webhooks"),
		EnabledEvents:        events,
		Name:                 types.StringValue("Enforcement actions"),
		Description:          types.StringUnknown(),
		Enabled:              types.BoolValue(true),
		DisabledReason:       types.StringUnknown(),
		SigningSecret:        types.StringUnknown(),
		SigningSecretVersion: types.Int64Value(1),
		CreatedAt:            types.StringUnknown(),
		UpdatedAt:            types.StringUnknown(),
	}
}

// stateWebhookModel is the fixture endpoint as stored after a create.
func stateWebhookModel(t *testing.T) OrganizationWebhookResourceModel {
	t.Helper()
	m := baseWebhookModel(t)
	m.ID = types.StringValue(testWebhookID)
	m.Description = types.StringNull()
	m.DisabledReason = types.StringNull()
	m.SigningSecret = types.StringValue("whsec_original")
	m.CreatedAt = types.StringValue("2026-10-20T19:00:00Z")
	m.UpdatedAt = types.StringValue("2026-10-20T19:00:00Z")
	return m
}

func webhookPlan(t *testing.T, r resource.Resource, m OrganizationWebhookResourceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: schematest.ResourceSchema(t, r)}
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("plan: %v", diags)
	}
	return p
}

func webhookState(t *testing.T, r resource.Resource, m OrganizationWebhookResourceModel) tfsdk.State {
	t.Helper()
	s := tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
	if diags := s.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return s
}

func readWebhookState(t *testing.T, s tfsdk.State) OrganizationWebhookResourceModel {
	t.Helper()
	var m OrganizationWebhookResourceModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	return m
}

// --- Create ---

func TestOrganizationWebhookResource_CreateCapturesSigningSecret(t *testing.T) {
	t.Parallel()

	api, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		return http.StatusOK, webhookJSON(t, map[string]any{"signing_secret": "whsec_created"})
	})
	r := &OrganizationWebhookResource{client: client}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: webhookPlan(t, r, baseWebhookModel(t))}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if got, want := api.calls(), []string{"POST /v1/organizations/webhooks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)

	body := api.request(t, 0).Body
	if body["url"] != "https://example.com/anthropic-webhooks" || body["name"] != "Enforcement actions" {
		t.Errorf("create body = %v", body)
	}
	if _, ok := body["description"]; ok {
		t.Errorf("an unset description must not be sent, got %v", body["description"])
	}
	if _, ok := body["state"]; ok {
		t.Errorf("the create body has no state field, got %v", body["state"])
	}
	if events, _ := body["enabled_events"].([]any); len(events) != 2 {
		t.Errorf("enabled_events = %v, want both event types", body["enabled_events"])
	}

	got := readWebhookState(t, resp.State)
	if got.SigningSecret.ValueString() != "whsec_created" {
		t.Errorf("signing_secret = %v, want the secret of the create response", got.SigningSecret)
	}
	if got.SigningSecretVersion.ValueInt64() != 1 {
		t.Errorf("signing_secret_version = %v, want the planned 1", got.SigningSecretVersion)
	}
	if got.ID.ValueString() != testWebhookID || !got.Enabled.ValueBool() {
		t.Errorf("id/enabled = %v/%v", got.ID, got.Enabled)
	}
	if !got.Description.IsNull() || !got.DisabledReason.IsNull() {
		t.Errorf("empty description/disabled_reason must map to null, got %v/%v", got.Description, got.DisabledReason)
	}
}

func TestOrganizationWebhookResource_CreateDisabledSendsSecondCall(t *testing.T) {
	t.Parallel()

	api, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		if r.Path == organizationWebhooksPath {
			return http.StatusOK, webhookJSON(t, map[string]any{"signing_secret": "whsec_created"})
		}
		return http.StatusOK, webhookJSON(t, map[string]any{"state": "disabled"})
	})
	r := &OrganizationWebhookResource{client: client}

	plan := baseWebhookModel(t)
	plan.Enabled = types.BoolValue(false)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: webhookPlan(t, r, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	want := []string{"POST /v1/organizations/webhooks", "POST /v1/organizations/webhooks/" + testWebhookID}
	if got := api.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)
	if body := api.request(t, 1).Body; !reflect.DeepEqual(body, map[string]any{"state": "disabled"}) {
		t.Errorf("disable body = %v, want only state=disabled", body)
	}

	got := readWebhookState(t, resp.State)
	if got.Enabled.ValueBool() {
		t.Error("enabled = true, want false after the disable call")
	}
	if got.SigningSecret.ValueString() != "whsec_created" {
		t.Errorf("signing_secret = %v, want the secret of the create response", got.SigningSecret)
	}
}

func TestOrganizationWebhookResource_CreateDisableFailureKeepsEndpoint(t *testing.T) {
	t.Parallel()

	_, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		if r.Path == organizationWebhooksPath {
			return http.StatusOK, webhookJSON(t, map[string]any{"signing_secret": "whsec_created"})
		}
		return http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"boom"}}`
	})
	r := &OrganizationWebhookResource{client: client}

	plan := baseWebhookModel(t)
	plan.Enabled = types.BoolValue(false)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: webhookPlan(t, r, plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the disable call fails")
	}

	got := readWebhookState(t, resp.State)
	if got.ID.ValueString() != testWebhookID || got.SigningSecret.ValueString() != "whsec_created" {
		t.Errorf("the created endpoint and its secret must be saved, got id=%v secret=%v", got.ID, got.SigningSecret)
	}
}

// --- Read ---

func TestOrganizationWebhookResource_ReadPreservesSigningSecret(t *testing.T) {
	t.Parallel()

	api, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		return http.StatusOK, webhookJSON(t, map[string]any{
			"name":            "",
			"state":           "disabled",
			"disabled_reason": "Deliveries kept failing.",
			"updated_at":      "2026-10-21T08:00:00Z",
		})
	})
	r := &OrganizationWebhookResource{client: client}

	prior := stateWebhookModel(t)
	prior.SigningSecretVersion = types.Int64Value(3)
	resp := resource.ReadResponse{State: webhookState(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: webhookState(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	if got, want := api.calls(), []string{"GET /v1/organizations/webhooks/" + testWebhookID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	api.assertBetaOnEveryRequest(t)

	got := readWebhookState(t, resp.State)
	if got.SigningSecret.ValueString() != "whsec_original" || got.SigningSecretVersion.ValueInt64() != 3 {
		t.Errorf("signing_secret/version = %v/%v, want them kept from prior state", got.SigningSecret, got.SigningSecretVersion)
	}
	if !got.Name.IsNull() {
		t.Errorf("name = %v, want null for the API's empty string", got.Name)
	}
	if got.Enabled.ValueBool() || got.DisabledReason.ValueString() != "Deliveries kept failing." {
		t.Errorf("enabled/disabled_reason = %v/%v, want the API's disabled state", got.Enabled, got.DisabledReason)
	}
	if got.UpdatedAt.ValueString() != "2026-10-21T08:00:00Z" {
		t.Errorf("updated_at = %v", got.UpdatedAt)
	}
}

func TestOrganizationWebhookResource_ReadAfterImportLeavesSecretNull(t *testing.T) {
	t.Parallel()

	_, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		return http.StatusOK, webhookJSON(t, nil)
	})
	r := &OrganizationWebhookResource{client: client}

	imported := schematest.NullState(t, r)
	resp := resource.ImportStateResponse{State: imported}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testWebhookID}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}

	readResp := resource.ReadResponse{State: resp.State}
	r.Read(context.Background(), resource.ReadRequest{State: resp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", readResp.Diagnostics)
	}

	got := readWebhookState(t, readResp.State)
	if got.ID.ValueString() != testWebhookID || got.URL.ValueString() != "https://example.com/anthropic-webhooks" {
		t.Errorf("id/url = %v/%v", got.ID, got.URL)
	}
	if !got.SigningSecret.IsNull() || !got.SigningSecretVersion.IsNull() {
		t.Errorf("signing_secret/version = %v/%v, want null after an import", got.SigningSecret, got.SigningSecretVersion)
	}
}

func TestOrganizationWebhookResource_ReadNotFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	_, client := newFakeWebhookAPI(t, func(recordedRequest) (int, string) { return notFound() })
	r := &OrganizationWebhookResource{client: client}

	state := webhookState(t, r, stateWebhookModel(t))
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

func TestOrganizationWebhookResource_UpdateRegeneratesOnVersionBump(t *testing.T) {
	t.Parallel()

	api, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		if strings.HasSuffix(r.Path, "/regenerate_signing_secret") {
			return http.StatusOK, `{"signing_secret":"whsec_rotated"}`
		}
		return http.StatusOK, webhookJSON(t, map[string]any{"updated_at": "2026-10-22T10:00:00Z"})
	})
	r := &OrganizationWebhookResource{client: client}

	state := stateWebhookModel(t)
	plan := state
	plan.SigningSecretVersion = types.Int64Value(2)
	plan.SigningSecret = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.DisabledReason = types.StringUnknown()

	resp := resource.UpdateResponse{State: webhookState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: webhookPlan(t, r, plan), State: webhookState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	want := []string{
		"POST /v1/organizations/webhooks/" + testWebhookID + "/regenerate_signing_secret",
		"GET /v1/organizations/webhooks/" + testWebhookID,
	}
	if got := api.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v (no update call when only the version changes)", got, want)
	}
	api.assertBetaOnEveryRequest(t)

	got := readWebhookState(t, resp.State)
	if got.SigningSecret.ValueString() != "whsec_rotated" || got.SigningSecretVersion.ValueInt64() != 2 {
		t.Errorf("signing_secret/version = %v/%v, want the regenerated secret and version 2", got.SigningSecret, got.SigningSecretVersion)
	}
	if got.UpdatedAt.ValueString() != "2026-10-22T10:00:00Z" {
		t.Errorf("updated_at = %v, want the value read back", got.UpdatedAt)
	}
}

func TestOrganizationWebhookResource_UpdateSendsOnlyChangedFields(t *testing.T) {
	t.Parallel()

	api, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		return http.StatusOK, webhookJSON(t, map[string]any{"name": "Renamed", "state": "disabled"})
	})
	r := &OrganizationWebhookResource{client: client}

	state := stateWebhookModel(t)
	plan := state
	plan.Name = types.StringValue("Renamed")
	plan.Enabled = types.BoolValue(false)
	plan.UpdatedAt = types.StringUnknown()
	plan.DisabledReason = types.StringUnknown()

	resp := resource.UpdateResponse{State: webhookState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: webhookPlan(t, r, plan), State: webhookState(t, r, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	if got, want := api.calls(), []string{"POST /v1/organizations/webhooks/" + testWebhookID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v (no regeneration without a version change)", got, want)
	}
	api.assertBetaOnEveryRequest(t)
	if body, want := api.request(t, 0).Body, map[string]any{"name": "Renamed", "state": "disabled"}; !reflect.DeepEqual(body, want) {
		t.Errorf("update body = %v, want %v", body, want)
	}

	got := readWebhookState(t, resp.State)
	if got.SigningSecret.ValueString() != "whsec_original" {
		t.Errorf("signing_secret = %v, want it kept from state", got.SigningSecret)
	}
	if got.Name.ValueString() != "Renamed" || got.Enabled.ValueBool() {
		t.Errorf("name/enabled = %v/%v", got.Name, got.Enabled)
	}
}

func TestOrganizationWebhookResource_UpdateRegenerateFailureKeepsOldSecret(t *testing.T) {
	t.Parallel()

	_, client := newFakeWebhookAPI(t, func(r recordedRequest) (int, string) {
		if strings.HasSuffix(r.Path, "/regenerate_signing_secret") {
			return http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"boom"}}`
		}
		return http.StatusOK, webhookJSON(t, map[string]any{"url": "https://example.com/new"})
	})
	r := &OrganizationWebhookResource{client: client}

	state := stateWebhookModel(t)
	plan := state
	plan.URL = types.StringValue("https://example.com/new")
	plan.SigningSecretVersion = types.Int64Value(2)
	plan.SigningSecret = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.DisabledReason = types.StringUnknown()

	resp := resource.UpdateResponse{State: webhookState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: webhookPlan(t, r, plan), State: webhookState(t, r, state)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the regeneration fails")
	}

	got := readWebhookState(t, resp.State)
	if got.URL.ValueString() != "https://example.com/new" {
		t.Errorf("url = %v, want the update that landed to be recorded", got.URL)
	}
	if got.SigningSecret.ValueString() != "whsec_original" || got.SigningSecretVersion.ValueInt64() != 1 {
		t.Errorf("signing_secret/version = %v/%v, want the old ones so the next plan retries", got.SigningSecret, got.SigningSecretVersion)
	}
}

func TestOrganizationWebhookResource_UpdateErrorKeepsPriorState(t *testing.T) {
	t.Parallel()

	_, client := newFakeWebhookAPI(t, func(recordedRequest) (int, string) {
		return http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"boom"}}`
	})
	r := &OrganizationWebhookResource{client: client}

	state := stateWebhookModel(t)
	plan := state
	plan.SigningSecretVersion = types.Int64Value(2)
	plan.SigningSecret = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.DisabledReason = types.StringUnknown()

	// The framework hands Update a response that starts as the prior state.
	resp := resource.UpdateResponse{State: webhookState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: webhookPlan(t, r, plan), State: webhookState(t, r, state)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the regeneration fails")
	}

	got := readWebhookState(t, resp.State)
	if got.SigningSecret.ValueString() != "whsec_original" || got.SigningSecretVersion.ValueInt64() != 1 {
		t.Errorf("signing_secret/version = %v/%v, want the prior state, not the plan's unknown secret", got.SigningSecret, got.SigningSecretVersion)
	}
}

// --- Delete ---

func TestOrganizationWebhookResource_Delete(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{"deleted": http.StatusOK, "already gone": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api, client := newFakeWebhookAPI(t, func(recordedRequest) (int, string) {
				if status == http.StatusNotFound {
					return notFound()
				}
				return status, `{"id":"` + testWebhookID + `","type":"webhook_endpoint_deleted"}`
			})
			r := &OrganizationWebhookResource{client: client}

			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: webhookState(t, r, stateWebhookModel(t))}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			if got, want := api.calls(), []string{"DELETE /v1/organizations/webhooks/" + testWebhookID}; !reflect.DeepEqual(got, want) {
				t.Errorf("calls = %v, want %v", got, want)
			}
			api.assertBetaOnEveryRequest(t)
		})
	}
}

// --- Helpers ---

func TestBuildOrganizationWebhookUpdate(t *testing.T) {
	t.Parallel()

	state := stateWebhookModel(t)

	t.Run("no change sends nothing", func(t *testing.T) {
		body, diags := buildOrganizationWebhookUpdate(context.Background(), state, state)
		if diags.HasError() || !body.isEmpty() {
			t.Errorf("body = %+v, diags = %v", body, diags)
		}
	})

	t.Run("removed name keeps the stored value", func(t *testing.T) {
		plan := state
		plan.Name = types.StringNull()
		body, _ := buildOrganizationWebhookUpdate(context.Background(), plan, state)
		if body.Name != nil {
			t.Errorf("name = %v, want it omitted", *body.Name)
		}
	})

	t.Run("events replace the whole set", func(t *testing.T) {
		plan := state
		plan.EnabledEvents = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("user_profile.enforcement_action_opened")})
		body, diags := buildOrganizationWebhookUpdate(context.Background(), plan, state)
		if diags.HasError() {
			t.Fatal(diags)
		}
		if !reflect.DeepEqual(body.EnabledEvents, []string{"user_profile.enforcement_action_opened"}) {
			t.Errorf("enabled_events = %v", body.EnabledEvents)
		}
	})

	t.Run("re-enabling sends state enabled", func(t *testing.T) {
		disabled := state
		disabled.Enabled = types.BoolValue(false)
		body, _ := buildOrganizationWebhookUpdate(context.Background(), state, disabled)
		if body.State != organizationWebhookStateEnabled {
			t.Errorf("state = %q, want enabled", body.State)
		}
	})
}

func TestSigningSecretPlanModifier(t *testing.T) {
	t.Parallel()

	r := &OrganizationWebhookResource{}
	stateModel := stateWebhookModel(t)

	cases := map[string]struct {
		stateVersion types.Int64
		planVersion  types.Int64
		stateSecret  types.String
		wantUnknown  bool
	}{
		"unchanged version keeps the secret":     {types.Int64Value(1), types.Int64Value(1), types.StringValue("whsec_original"), false},
		"bumped version regenerates":             {types.Int64Value(1), types.Int64Value(2), types.StringValue("whsec_original"), true},
		"first version on an existing endpoint":  {types.Int64Null(), types.Int64Value(1), types.StringValue("whsec_original"), true},
		"removed version keeps the secret":       {types.Int64Value(1), types.Int64Null(), types.StringValue("whsec_original"), false},
		"unknown version may regenerate":         {types.Int64Value(1), types.Int64Unknown(), types.StringValue("whsec_original"), true},
		"imported endpoint keeps a null secret":  {types.Int64Null(), types.Int64Null(), types.StringNull(), false},
		"import then first version captures one": {types.Int64Null(), types.Int64Value(1), types.StringNull(), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := stateModel
			st.SigningSecretVersion = tc.stateVersion
			st.SigningSecret = tc.stateSecret
			pl := st
			pl.SigningSecretVersion = tc.planVersion
			pl.SigningSecret = types.StringUnknown()

			req := planmodifier.StringRequest{
				Path:       path.Root("signing_secret"),
				Plan:       webhookPlan(t, r, pl),
				State:      webhookState(t, r, st),
				PlanValue:  types.StringUnknown(),
				StateValue: tc.stateSecret,
			}
			resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
			signingSecretPlanModifier{}.PlanModifyString(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if tc.wantUnknown {
				if !resp.PlanValue.IsUnknown() {
					t.Errorf("plan = %v, want unknown", resp.PlanValue)
				}
				return
			}
			if !resp.PlanValue.Equal(tc.stateSecret) {
				t.Errorf("plan = %v, want the state value %v", resp.PlanValue, tc.stateSecret)
			}
		})
	}

	t.Run("create leaves the secret unknown", func(t *testing.T) {
		t.Parallel()
		req := planmodifier.StringRequest{
			Path: path.Root("signing_secret"),
			Plan: webhookPlan(t, r, baseWebhookModel(t)),
			State: tfsdk.State{
				Schema: schematest.ResourceSchema(t, r),
				Raw:    tftypes.NewValue(schematest.ResourceObjectType(t, r), nil),
			},
			PlanValue: types.StringUnknown(),
		}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		signingSecretPlanModifier{}.PlanModifyString(context.Background(), req, &resp)
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("plan = %v, want unknown on create", resp.PlanValue)
		}
	})
}

func TestOrganizationWebhookResource_EnabledEventsValidator(t *testing.T) {
	t.Parallel()

	eventsAttr, ok := schematest.ResourceSchema(t, &OrganizationWebhookResource{}).Attributes["enabled_events"].(schema.SetAttribute)
	if !ok {
		t.Fatal("enabled_events is not a set attribute")
	}

	validate := func(events ...string) bool {
		v, _ := types.SetValueFrom(context.Background(), types.StringType, append([]string{}, events...))
		req := validator.SetRequest{Path: path.Root("enabled_events"), ConfigValue: v}
		resp := validator.SetResponse{}
		for _, val := range eventsAttr.Validators {
			val.ValidateSet(context.Background(), req, &resp)
		}
		return !resp.Diagnostics.HasError()
	}

	for _, e := range organizationWebhookEventTypes {
		if !validate(e) {
			t.Errorf("event type %q rejected", e)
		}
	}
	if validate("user_profile.created") {
		t.Error("an undocumented event type was accepted")
	}
	if validate() {
		t.Error("an empty set was accepted")
	}
}
