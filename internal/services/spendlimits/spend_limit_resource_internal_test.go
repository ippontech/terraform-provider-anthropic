// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Setting spend limits needs a Claude Enterprise organization (user limits) or
// the Console early access preview (organization and workspace limits); the
// test organization has neither, so CRUD wiring and mapping are covered here
// against httptest servers and the live tests are opt-in.
package spendlimits

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const (
	spendLimitID = "spl_01AbCdEfGhIjKlMnOpQrSt"
	userID       = "user_01AbCdEfGh"
	workspaceID  = "wrkspc_01AbCdEfGh"
)

func userLimitJSON(amount string) string {
	return `{"type":"spend_limit","id":"` + spendLimitID + `","amount":` + amount + `,"currency":"USD","is_enabled":true,"period":"monthly",` +
		`"scope":{"type":"user","user_id":"` + userID + `"},"created_at":"2026-06-01T00:00:00Z","updated_at":"2026-06-02T00:00:00Z"}`
}

func resourcePlan(t *testing.T, r resource.Resource, scope tftypes.Value, period string, amount any) tfsdk.Plan {
	t.Helper()
	return tfsdk.Plan{
		Raw: resourceValue(t, r, map[string]tftypes.Value{
			"id":         str(tftypes.UnknownValue),
			"scope":      scope,
			"period":     str(period),
			"amount":     str(amount),
			"currency":   str(tftypes.UnknownValue),
			"is_enabled": tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
			"created_at": str(tftypes.UnknownValue),
			"updated_at": str(tftypes.UnknownValue),
		}),
		Schema: schematest.ResourceSchema(t, r),
	}
}

func stateWithID(t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()
	return tfsdk.State{
		Raw:    resourceValue(t, r, map[string]tftypes.Value{"id": str(spendLimitID), "scope": scopeValue(t, r, "user", userID, ""), "period": str("monthly")}),
		Schema: schematest.ResourceSchema(t, r),
	}
}

func spendLimitState(t *testing.T, s tfsdk.State) (SpendLimitResourceModel, spendLimitScopeModel) {
	t.Helper()
	var m SpendLimitResourceModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	var scope spendLimitScopeModel
	if diags := m.Scope.As(context.Background(), &scope, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("scope: %v", diags)
	}
	return m, scope
}

// --- Create ---

func TestSpendLimitResource_Create_userLimit(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, userLimitJSON(`"50000"`) })
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, scopeValue(t, r, "user", userID, ""), "monthly", "50000")
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/spend_limits" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if req.Beta != "" {
		t.Errorf("anthropic-beta = %q on a set, want none", req.Beta)
	}
	scope, _ := req.Body["scope"].(map[string]any)
	if scope["type"] != "user" || scope["user_id"] != userID || len(scope) != 2 {
		t.Errorf("body scope = %v, want only type and user_id", scope)
	}
	if req.Body["period"] != "monthly" || req.Body["amount"] != "50000" {
		t.Errorf("body = %v", req.Body)
	}

	got, gotScope := spendLimitState(t, resp.State)
	if got.ID.ValueString() != spendLimitID || got.Amount.ValueString() != "50000" || got.Currency.ValueString() != "USD" || !got.IsEnabled.ValueBool() {
		t.Errorf("state = %+v", got)
	}
	if gotScope.UserID.ValueString() != userID || !gotScope.WorkspaceID.IsNull() {
		t.Errorf("scope = %+v", gotScope)
	}
}

func TestSpendLimitResource_Create_nullAmountIsAnExplicitNoLimitOverride(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, userLimitJSON(`null`) })
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, scopeValue(t, r, "user", userID, ""), "weekly", nil)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	body := rec.all()[0].Body
	if amount, ok := body["amount"]; !ok || amount != nil {
		t.Errorf("amount = %v (present: %v), want an explicit null", amount, ok)
	}
	if body["period"] != "weekly" {
		t.Errorf("period = %v", body["period"])
	}
	if got, _ := spendLimitState(t, resp.State); !got.Amount.IsNull() {
		t.Errorf("amount = %v, want null", got.Amount)
	}
}

func TestSpendLimitResource_Create_workspaceLimit(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"type":"spend_limit","id":"spl_ws","amount":"100000","currency":"USD","is_enabled":true,"period":"monthly",` +
			`"scope":{"type":"workspace","workspace_id":"` + workspaceID + `"},"created_at":"2026-06-01T00:00:00Z","updated_at":"2026-06-01T00:00:00Z"}`
	})
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, scopeValue(t, r, "workspace", "", workspaceID), "monthly", "100000")
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	scope, _ := rec.all()[0].Body["scope"].(map[string]any)
	if scope["type"] != "workspace" || scope["workspace_id"] != workspaceID || len(scope) != 2 {
		t.Errorf("body scope = %v, want only type and workspace_id", scope)
	}
	if _, gotScope := spendLimitState(t, resp.State); gotScope.WorkspaceID.ValueString() != workspaceID || !gotScope.UserID.IsNull() {
		t.Errorf("scope = %+v", gotScope)
	}
}

func TestSpendLimitResource_Create_apiErrorIsReported(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"organization_type_not_supported"}}`
	})
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, scopeValue(t, r, "organization", "", ""), "monthly", "100000")
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a 400")
	}
}

// --- Update ---

func TestSpendLimitResource_Update_resetsTheAmount(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, userLimitJSON(`"75000"`) })
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	state := stateWithID(t, r)
	plan := resourcePlan(t, r, scopeValue(t, r, "user", userID, ""), "monthly", "75000")
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/spend_limits" || req.Body["amount"] != "75000" {
		t.Errorf("request = %s %s %v: an update is the same upsert", req.Method, req.Path, req.Body)
	}
	if got, _ := spendLimitState(t, resp.State); got.Amount.ValueString() != "75000" || got.ID.ValueString() != spendLimitID {
		t.Errorf("state = %+v", got)
	}
}

// --- Read ---

func TestSpendLimitResource_Read(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, userLimitJSON(`"50000"`) })
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	// An imported limit: only the ID is known.
	state := tfsdk.State{Raw: resourceValue(t, r, map[string]tftypes.Value{"id": str(spendLimitID)}), Schema: schematest.ResourceSchema(t, r)}
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodGet || req.Path != "/v1/organizations/spend_limits/"+spendLimitID {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if req.Beta != "" {
		t.Errorf("anthropic-beta = %q on a get, want none", req.Beta)
	}

	got, gotScope := spendLimitState(t, resp.State)
	if got.Period.ValueString() != "monthly" || got.Amount.ValueString() != "50000" || gotScope.Type.ValueString() != "user" || gotScope.UserID.ValueString() != userID {
		t.Errorf("state = %+v, scope = %+v", got, gotScope)
	}
}

func TestSpendLimitResource_Read_notFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) { return http.StatusNotFound, notFoundBody })
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	state := stateWithID(t, r)
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state after a 404")
	}
}

// --- Delete ---

func TestSpendLimitResource_Delete(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{"deleted": http.StatusOK, "already gone": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, rec := stub(t, func(recordedRequest) (int, string) {
				if status == http.StatusNotFound {
					return status, notFoundBody
				}
				return status, `{"id":"` + spendLimitID + `","type":"spend_limit_deleted"}`
			})
			r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

			state := stateWithID(t, r)
			resp := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			req := rec.all()[0]
			if req.Method != http.MethodDelete || req.Path != "/v1/organizations/spend_limits/"+spendLimitID {
				t.Errorf("request = %s %s", req.Method, req.Path)
			}
			assertBetaQuery(t, req)
		})
	}
}

// The set endpoint upserts on (scope, period), so a create can adopt the very
// limit another instance is about to destroy: `count` going from [A, B] to
// [B] replaces [0] (A's limit deleted, B's pair set, the upsert returning the
// limit [1] tracks) while [1] is destroyed, and a create_before_destroy
// replacement sets the pair before deleting its old object. The delete must
// not then remove the limit the other write now manages: the member would be
// left without a cap while state still tracks it.
func TestSpendLimitResource_Delete_leavesALimitSetAgainInTheSameApply(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(req recordedRequest) (int, string) {
		if req.Method == http.MethodDelete {
			return http.StatusOK, `{"id":"` + spendLimitID + `","type":"spend_limit_deleted"}`
		}
		return http.StatusOK, userLimitJSON(`"50000"`)
	})
	client := admintest.NewClient(t, srv)

	// Instance [0] sets the pair: the upsert answers with the existing limit.
	creator := &SpendLimitResource{client: client}
	plan := resourcePlan(t, creator, scopeValue(t, creator, "user", userID, ""), "monthly", "50000")
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	creator.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", createResp.Diagnostics)
	}

	// Instance [1], which tracked that limit, is destroyed.
	destroyed := &SpendLimitResource{client: client}
	state := stateWithID(t, destroyed)
	deleteResp := resource.DeleteResponse{State: state}
	destroyed.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", deleteResp.Diagnostics)
	}
	for _, req := range rec.all() {
		if req.Method == http.MethodDelete {
			t.Fatalf("DELETE %s sent: it removes the cap instance [0] just set and still tracks", req.Path)
		}
	}
	if deleteResp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("diagnostics = %v, want one warning saying the limit was left in place", deleteResp.Diagnostics)
	}
}

// An empty identifier (an empty variable, a lookup that found nothing) must
// fail validation: the request would otherwise drop it (omitempty) and the
// API answer 400 in the middle of the apply.
func TestSpendLimitResource_scopeIdentifiersRejectAnEmptyString(t *testing.T) {
	t.Parallel()

	scope := schematest.ResourceSchema(t, &SpendLimitResource{}).Attributes["scope"].(schema.SingleNestedAttribute)
	for _, name := range []string{"user_id", "workspace_id"} {
		attr := scope.Attributes[name].(schema.StringAttribute)
		var diags diag.Diagnostics
		for _, v := range attr.Validators {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("scope").AtName(name),
				ConfigValue: types.StringValue(""),
			}, &resp)
			diags.Append(resp.Diagnostics...)
		}
		if !diags.HasError() {
			t.Errorf("scope.%s = \"\" passes validation, want an error", name)
		}
	}
}

func TestSpendLimitResource_Delete_reportsOtherErrors(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"usage_tier limits cannot be deleted"}}`
	})
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}

	state := stateWithID(t, r)
	resp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a 400")
	}
}

// --- ImportState ---

func TestSpendLimitResource_ImportState(t *testing.T) {
	t.Parallel()

	r := &SpendLimitResource{}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: spendLimitID}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var id string
	resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if id != spendLimitID {
		t.Errorf("id = %q", id)
	}
}

// --- Validation ---

func TestSpendLimitConfigValidator(t *testing.T) {
	t.Parallel()

	r := &SpendLimitResource{}
	unknown := tftypes.UnknownValue

	cases := []struct {
		name    string
		scope   tftypes.Value
		period  any
		amount  any
		wantErr bool
	}{
		{"user limit", scopeValue(t, r, "user", userID, ""), nil, "50000", false},
		{"user no-limit override", scopeValue(t, r, "user", userID, ""), "daily", nil, false},
		{"user without user_id", scopeValue(t, r, "user", "", ""), nil, "50000", true},
		{"user with workspace_id", scopeValue(t, r, "user", userID, workspaceID), nil, "50000", true},
		{"workspace limit", scopeValue(t, r, "workspace", "", workspaceID), "monthly", "100000", false},
		{"workspace without workspace_id", scopeValue(t, r, "workspace", "", ""), nil, "100000", true},
		{"workspace with user_id", scopeValue(t, r, "workspace", userID, workspaceID), nil, "100000", true},
		{"organization limit", scopeValue(t, r, "organization", "", ""), nil, "100000", false},
		{"organization weekly", scopeValue(t, r, "organization", "", ""), "weekly", "100000", true},
		{"organization without amount", scopeValue(t, r, "organization", "", ""), nil, nil, true},
		{"unknown user_id", scopeValue(t, r, "user", unknown, ""), nil, "50000", false},
		{"unknown scope type", scopeValue(t, r, unknown, "", workspaceID), nil, nil, false},
		{"unknown amount on organization", scopeValue(t, r, "organization", "", ""), nil, unknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := tfsdk.Config{
				Raw:    resourceValue(t, r, map[string]tftypes.Value{"scope": tc.scope, "period": str(tc.period), "amount": str(tc.amount)}),
				Schema: schematest.ResourceSchema(t, r),
			}
			var resp resource.ValidateConfigResponse
			spendLimitConfigValidator{}.ValidateResource(context.Background(), resource.ValidateConfigRequest{Config: config}, &resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestAmountPattern(t *testing.T) {
	t.Parallel()

	for amount, want := range map[string]bool{"0": true, "50000": true, "050000": false, "-1": false, "500.00": false, "": false, "1e3": false} {
		if got := amountPattern.MatchString(amount); got != want {
			t.Errorf("amountPattern(%q) = %v, want %v", amount, got, want)
		}
	}
}

// assertAppliedAsPlanned fails for every top-level attribute the plan holds
// as a known value that the applied state does not: Terraform reports those
// as "Provider produced inconsistent result after apply".
func assertAppliedAsPlanned(t *testing.T, plan tfsdk.Plan, state tfsdk.State) {
	t.Helper()
	var planned, applied map[string]tftypes.Value
	if err := plan.Raw.As(&planned); err != nil {
		t.Fatal(err)
	}
	if err := state.Raw.As(&applied); err != nil {
		t.Fatal(err)
	}
	for name, p := range planned {
		if p.IsFullyKnown() && !p.Equal(applied[name]) {
			t.Errorf("%s: planned %s, applied %s", name, p, applied[name])
		}
	}
}

// The upsert creates a limit under a new ID when the tracked one is gone: an
// override removed in claude.ai after a saved plan, `-refresh=false`, or
// another instance on the same pair destroyed first. An amount change cannot
// promise the prior id and created_at, so the plan must leave them unknown
// for Update to record the limit the set answered with.
func TestSpendLimitResource_Update_acceptsTheLimitTheUpsertReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const newID = "spl_02NewLimitAfterUpsert"
	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"type":"spend_limit","id":"` + newID + `","amount":"75000","currency":"USD","is_enabled":true,"period":"monthly",` +
			`"scope":{"type":"user","user_id":"` + userID + `"},"created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-01T00:00:00Z"}`
	})
	r := &SpendLimitResource{client: admintest.NewClient(t, srv)}
	sch := schematest.ResourceSchema(t, r)

	// The state as the last refresh saw the limit.
	var limit spendLimitAPIResponse
	if err := json.Unmarshal([]byte(userLimitJSON(`"50000"`)), &limit); err != nil {
		t.Fatal(err)
	}
	prior, diags := mapSpendLimitToState(limit)
	state := tfsdk.State{Schema: sch}
	diags.Append(state.Set(ctx, &prior)...)

	// The plan the framework builds for an amount change: every computed
	// attribute without configuration unknown, then the plan modifiers.
	planned := prior
	planned.Amount = types.StringValue("75000")
	planned.ID, planned.Currency, planned.CreatedAt, planned.UpdatedAt = types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	planned.IsEnabled = types.BoolUnknown()
	plan := tfsdk.Plan{Schema: sch}
	diags.Append(plan.Set(ctx, &planned)...)
	for _, name := range []string{"id", "currency", "created_at", "updated_at"} {
		var stateValue types.String
		diags.Append(state.GetAttribute(ctx, path.Root(name), &stateValue)...)
		value := types.StringUnknown()
		for _, m := range sch.Attributes[name].(schema.StringAttribute).PlanModifiers {
			resp := planmodifier.StringResponse{PlanValue: value}
			m.PlanModifyString(ctx, planmodifier.StringRequest{
				Path: path.Root(name), Plan: plan, State: state, StateValue: stateValue, PlanValue: value, ConfigValue: types.StringNull(),
			}, &resp)
			value = resp.PlanValue
		}
		diags.Append(plan.SetAttribute(ctx, path.Root(name), value)...)
	}
	if diags.HasError() {
		t.Fatal(diags)
	}

	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	assertAppliedAsPlanned(t, plan, resp.State)
	if got, _ := spendLimitState(t, resp.State); got.ID.ValueString() != newID {
		t.Errorf("id = %s, want the limit the upsert answered with", got.ID)
	}
}
