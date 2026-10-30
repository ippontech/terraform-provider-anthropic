// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"

	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	provretry "github.com/ippontech/terraform-provider-anthropic/internal/retry"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Models of the lifecycle tests, with the dates of
// https://platform.claude.com/docs/en/about-claude/model-deprecations.
const (
	activeModel     = "claude-sonnet-4-6"
	activeAdvisor   = "claude-opus-4-6"
	deprecatedModel = "claude-sonnet-4-5-20250929"
	retiredModel    = "claude-opus-4-1-20250805"
	modelRoute      = "GET /v1/models/{model_id}"
)

// lifecycleCatalogue is what the Models API answers for each model ID or
// alias (https://platform.claude.com/docs/en/api/models/retrieve). An alias
// answers the model it resolves to, whose id differs from the one asked for.
var lifecycleCatalogue = map[string]map[string]any{
	activeModel:             modelInfo(activeModel, "active", nil, nil),
	activeAdvisor:           modelInfo(activeAdvisor, "active", nil, nil),
	deprecatedModel:         modelInfo(deprecatedModel, "deprecated", "2026-09-30T00:00:00Z", "2026-11-30T00:00:00Z"),
	"claude-mythos-preview": modelInfo("claude-mythos-preview", "deprecated", "2026-06-09T00:00:00Z", nil),
	"claude-sonnet-4-5":     modelInfo(deprecatedModel, "deprecated", "2026-09-30T00:00:00Z", "2026-11-30T00:00:00Z"),
	retiredModel:            modelInfo(retiredModel, "retired", "2026-06-05T00:00:00Z", "2026-08-05T00:00:00Z"),
}

func modelInfo(id, lifecycle string, deprecatedAt, retiresAt any) map[string]any {
	return map[string]any{
		"type": "model", "id": id, "display_name": id, "created_at": "2025-01-01T00:00:00Z",
		"lifecycle": lifecycle, "deprecated_at": deprecatedAt, "retires_at": retiresAt,
	}
}

// newModelsServer serves lifecycleCatalogue on the Models API "get"; another
// ID is a 404 not_found_error.
func newModelsServer(t *testing.T) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	srv.Handle(modelRoute, func(w http.ResponseWriter, r *http.Request) {
		if m, ok := lifecycleCatalogue[r.PathValue("model_id")]; ok {
			sdktest.WriteJSON(w, http.StatusOK, m)
			return
		}
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "model: "+r.PathValue("model_id"))
	})
	return srv
}

// lifecycleResource is an agent resource on srv with the production retry
// policy (shorter delays), configured with pd, so that it shares pd's cache.
func lifecycleResource(t *testing.T, srv *sdktest.Server, pd *providerdata.ProviderData) *AgentResource {
	t.Helper()
	policy := provretry.Policy{MaxRetries: provretry.DefaultMaxRetries, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	if pd.Client == nil {
		pd.Client = sdktest.NewAPIKeyClient(t, srv.Server, provretry.SDKOptions(policy)...)
	}
	r := &AgentResource{}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: pd}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

func agentModel(id string) []modelRef {
	return []modelRef{{path: path.Root("model"), slot: modelSlotAgent, value: types.StringValue(id)}}
}

// wantDiag is the one diagnostic a check must return, or none when summary
// is "".
type wantDiag struct {
	severity diag.Severity
	summary  string
	path     path.Path
	detail   []string // substrings
	absent   []string // substrings the detail must not contain
}

func assertDiag(t *testing.T, diags diag.Diagnostics, want wantDiag) {
	t.Helper()
	if want.summary == "" {
		if len(diags) != 0 {
			t.Fatalf("diagnostics = %v, want none", diags)
		}
		return
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want one %q", diags, want.summary)
	}
	d := diags[0]
	if d.Severity() != want.severity || d.Summary() != want.summary {
		t.Errorf("diagnostic = %s %q, want %s %q", d.Severity(), d.Summary(), want.severity, want.summary)
	}
	if wp, ok := d.(diag.DiagnosticWithPath); !ok || !wp.Path().Equal(want.path) {
		t.Errorf("diagnostic path = %v, want %s", d, want.path)
	}
	for _, s := range want.detail {
		if !strings.Contains(d.Detail(), s) {
			t.Errorf("detail %q does not contain %q", d.Detail(), s)
		}
	}
	for _, s := range want.absent {
		if strings.Contains(d.Detail(), s) {
			t.Errorf("detail %q contains %q", d.Detail(), s)
		}
	}
}

// Each lifecycle, on create, on a change of model and with the model kept.
// Every case looks the model up exactly once.
func TestCheckModelLifecycle(t *testing.T) {
	t.Parallel()
	model := path.Root("model")
	for name, tc := range map[string]struct {
		model string
		prior map[string]string
		want  wantDiag
	}{
		"deprecated": {deprecatedModel, nil, wantDiag{
			severity: diag.SeverityWarning, summary: "Deprecated Model", path: model,
			detail: []string{
				`"claude-sonnet-4-5-20250929" is deprecated`,
				"deprecated_at 2026-09-30T00:00:00Z, retires_at 2026-11-30T00:00:00Z",
				"Requests naming a model fail once it is retired, so this agent will stop working then: move it to an active model",
				modelDeprecationsURL,
			},
		}},
		"deprecated, kept": {deprecatedModel, map[string]string{modelSlotAgent: deprecatedModel}, wantDiag{
			severity: diag.SeverityWarning, summary: "Deprecated Model", path: model,
		}},
		"deprecated without retirement date": {"claude-mythos-preview", nil, wantDiag{
			severity: diag.SeverityWarning, summary: "Deprecated Model", path: model,
			detail: []string{`"claude-mythos-preview" is deprecated (deprecated_at 2026-06-09T00:00:00Z)`},
			absent: []string{"retires_at"},
		}},
		"deprecated alias": {"claude-sonnet-4-5", nil, wantDiag{
			severity: diag.SeverityWarning, summary: "Deprecated Model", path: model,
			detail: []string{`"claude-sonnet-4-5" (resolved to "claude-sonnet-4-5-20250929") is deprecated`},
		}},
		"retired on create": {retiredModel, nil, wantDiag{
			severity: diag.SeverityError, summary: "Retired Model", path: model,
			detail: []string{
				`"claude-opus-4-1-20250805" is retired (deprecated_at 2026-06-05T00:00:00Z, retires_at 2026-08-05T00:00:00Z)`,
				"requests naming a retired model fail, so applying this plan would fail. Choose an active model",
			},
		}},
		"retired when changed": {retiredModel, map[string]string{modelSlotAgent: activeModel}, wantDiag{
			severity: diag.SeverityError, summary: "Retired Model", path: model,
		}},
		// The same model in another slot is not this one kept.
		"retired, held by the advisor only": {retiredModel, map[string]string{modelSlotAdvisor: retiredModel}, wantDiag{
			severity: diag.SeverityError, summary: "Retired Model", path: model,
		}},
		"retired when unchanged": {retiredModel, map[string]string{modelSlotAgent: retiredModel}, wantDiag{
			severity: diag.SeverityWarning, summary: "Retired Model", path: model,
			detail: []string{
				`"claude-opus-4-1-20250805" is retired`,
				"this agent can no longer run. Changes to other attributes still apply; move it to an active model",
			},
		}},
		"active":                {activeModel, nil, wantDiag{}},
		"active when unchanged": {activeModel, map[string]string{modelSlotAgent: activeModel}, wantDiag{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newModelsServer(t)
			r := lifecycleResource(t, srv, &providerdata.ProviderData{})
			diags := checkModelLifecycle(context.Background(), r.lookupModels(modelLookupTimeout), agentModel(tc.model), tc.prior)
			assertDiag(t, diags, tc.want)
			srv.AssertCalled(modelRoute, 1)
			srv.AssertNoBeta(modelRoute)
			srv.AssertCredential(modelRoute, sdktest.APIKey)
		})
	}
}

// A null or unknown model is skipped, without a lookup or a log; the known
// one next to them is checked.
func TestCheckModelLifecycle_nullOrUnknown(t *testing.T) {
	t.Parallel()
	srv := newModelsServer(t)
	r := lifecycleResource(t, srv, &providerdata.ProviderData{})
	refs := []modelRef{
		{path: path.Root("model"), slot: modelSlotAgent, value: types.StringUnknown()},
		{path: path.Root("multiagent").AtName("advisor").AtName("model"), slot: modelSlotAdvisor, value: types.StringNull()},
		{path: path.Root("multiagent").AtName("agents").AtListIndex(0).AtName("model"), slot: modelSlotRosterAdvisor, value: types.StringValue(retiredModel)},
	}
	var logs bytes.Buffer
	ctx := tflogtest.RootLogger(context.Background(), &logs)
	diags := checkModelLifecycle(ctx, r.lookupModels(modelLookupTimeout), refs, nil)
	assertDiag(t, diags, wantDiag{severity: diag.SeverityError, summary: "Retired Model", path: refs[2].path})
	if got := srv.Requests(modelRoute); len(got) != 1 || got[0].Path != "/v1/models/"+retiredModel {
		t.Errorf("lookups = %v, want only %s", got, retiredModel)
	}
	if logs.Len() != 0 {
		t.Errorf("logs = %s, want none", logs.String())
	}
}

// A failed lookup produces no diagnostic, even for a retired model, and is
// logged at debug level: 404, 401, 403, a 5xx after the retries, a dropped
// connection, and a lookup past its timeout.
func TestCheckModelLifecycle_lookupFailure(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		fail    func(srv *sdktest.Server)
		calls   int
		timeout time.Duration
	}{
		"404":   {fail: func(srv *sdktest.Server) { srv.FailNext(modelRoute, http.StatusNotFound, 1) }, calls: 1},
		"401":   {fail: func(srv *sdktest.Server) { srv.FailNext(modelRoute, http.StatusUnauthorized, 1) }, calls: 1},
		"403":   {fail: func(srv *sdktest.Server) { srv.FailNext(modelRoute, http.StatusForbidden, 1) }, calls: 1},
		"503":   {fail: func(srv *sdktest.Server) { srv.FailNext(modelRoute, http.StatusServiceUnavailable, 3) }, calls: 3},
		"reset": {fail: func(srv *sdktest.Server) { srv.ResetNext(modelRoute, 3) }, calls: 3},
		"timeout": {
			fail: func(srv *sdktest.Server) {
				srv.Handle("GET /v1/models/slow-model", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			},
			calls: 1, timeout: 50 * time.Millisecond,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newModelsServer(t)
			tc.fail(srv)
			model, route := retiredModel, modelRoute
			timeout := modelLookupTimeout
			if tc.timeout != 0 {
				model, route, timeout = "slow-model", "GET /v1/models/slow-model", tc.timeout
			}
			r := lifecycleResource(t, srv, &providerdata.ProviderData{})
			var logs bytes.Buffer
			ctx := tflogtest.RootLogger(context.Background(), &logs)

			start := time.Now()
			diags := checkModelLifecycle(ctx, r.lookupModels(timeout), agentModel(model), nil)
			if elapsed := time.Since(start); tc.timeout != 0 && elapsed > 2*time.Second {
				t.Errorf("lookup took %s, want about %s", elapsed, tc.timeout)
			}
			assertDiag(t, diags, wantDiag{})
			if got := len(srv.Requests(route)); got < 1 || got > tc.calls {
				t.Errorf("%d lookups, want 1 to %d", got, tc.calls)
			}
			entries, err := tflogtest.MultilineJSONDecode(&logs)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0]["@level"] != "debug" || entries[0]["model"] != model ||
				entries[0]["@message"] != "Model lifecycle check skipped: the Models API lookup failed" || entries[0]["error"] == "" {
				t.Errorf("logs = %v, want one debug entry for %s with its error", entries, model)
			}
		})
	}
}

// createPlan is the ModifyPlan request of a create of an agent on model.
func createPlan(t *testing.T, r *AgentResource, model string) resource.ModifyPlanRequest {
	t.Helper()
	return resource.ModifyPlanRequest{
		Plan:  schematest.Plan(t, r, map[string]any{"model": model, "name": "a"}),
		State: schematest.NullState(t, r),
	}
}

func runModifyPlan(r *AgentResource, req resource.ModifyPlanRequest) diag.Diagnostics {
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	return resp.Diagnostics
}

func modifyPlan(t *testing.T, r *AgentResource, model string) diag.Diagnostics {
	t.Helper()
	return runModifyPlan(r, createPlan(t, r, model))
}

// Two agents of one provider configuration, planned concurrently as
// Terraform does, make one request for their model and each gets its
// warning. Another configuration has a cache of its own.
func TestModifyPlan_cachePerConfiguration(t *testing.T) {
	t.Parallel()
	srv := newModelsServer(t)
	pd := &providerdata.ProviderData{}
	agents := []*AgentResource{lifecycleResource(t, srv, pd), lifecycleResource(t, srv, pd)}
	reqs := []resource.ModifyPlanRequest{createPlan(t, agents[0], deprecatedModel), createPlan(t, agents[1], deprecatedModel)}

	var wg sync.WaitGroup
	results := make([]diag.Diagnostics, len(agents))
	for i, r := range agents {
		wg.Go(func() { results[i] = runModifyPlan(r, reqs[i]) })
	}
	wg.Wait()
	for _, diags := range results {
		assertDiag(t, diags, wantDiag{severity: diag.SeverityWarning, summary: "Deprecated Model", path: path.Root("model")})
	}
	srv.AssertCalled(modelRoute, 1)

	other := lifecycleResource(t, srv, &providerdata.ProviderData{Client: pd.Client})
	assertDiag(t, modifyPlan(t, other, deprecatedModel), wantDiag{severity: diag.SeverityWarning, summary: "Deprecated Model", path: path.Root("model")})
	srv.AssertCalled(modelRoute, 2)
}

// ModifyPlan checks nothing on a destroy, or before the provider is
// configured; it checks a create.
func TestModifyPlan_skips(t *testing.T) {
	t.Parallel()
	srv := newModelsServer(t)
	r := lifecycleResource(t, srv, &providerdata.ProviderData{})

	destroy := resource.ModifyPlanRequest{
		Plan:  tfsdk.Plan{Raw: schematest.NullState(t, r).Raw, Schema: schematest.ResourceSchema(t, r)},
		State: schematest.State(t, r, map[string]any{"model": retiredModel, "name": "a"}),
	}
	var resp resource.ModifyPlanResponse
	r.ModifyPlan(context.Background(), destroy, &resp)
	assertDiag(t, resp.Diagnostics, wantDiag{})
	assertDiag(t, modifyPlan(t, &AgentResource{}, retiredModel), wantDiag{})
	srv.AssertCalled(modelRoute, 0)

	assertDiag(t, modifyPlan(t, r, retiredModel), wantDiag{severity: diag.SeverityError, summary: "Retired Model", path: path.Root("model")})
	srv.AssertCalled(modelRoute, 1)
}

// multiagentValue is a multiagent object with advisor (a model, or "" for
// none) and roster entries, each "agent:<id>" or "advisor:<model>".
func multiagentValue(t *testing.T, typ, advisor string, roster ...string) types.Object {
	t.Helper()
	entryType := types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}
	agents := types.ListNull(entryType)
	if roster != nil {
		var elems []attr.Value
		for _, e := range roster {
			kind, value, _ := strings.Cut(e, ":")
			id, model := types.StringNull(), types.StringNull()
			if kind == multiagentEntryAgent {
				id = types.StringValue(value)
			} else {
				model = types.StringValue(value)
			}
			elems = append(elems, types.ObjectValueMust(agentMultiagentEntryAttrTypes, map[string]attr.Value{
				"type": types.StringValue(kind), "id": id, "version": types.Int64Null(), "model": model,
			}))
		}
		agents = types.ListValueMust(entryType, elems)
	}
	adv := types.ObjectNull(agentMultiagentAdvisorAttrTypes)
	if advisor != "" {
		adv = types.ObjectValueMust(agentMultiagentAdvisorAttrTypes, map[string]attr.Value{"model": types.StringValue(advisor)})
	}
	return types.ObjectValueMust(agentMultiagentAttrTypes, map[string]attr.Value{
		"type":      types.StringValue(typ),
		"agents":    agents,
		"subagents": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"workflows": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"advisor":   adv,
	})
}

// The advisor models are checked like the agent's model: the
// multiagent_20261001 advisor, and the advisor entry of a coordinator roster,
// matched in the prior state by kind rather than by index.
func TestModifyPlan_advisorModels(t *testing.T) {
	t.Parallel()
	advisor := path.Root("multiagent").AtName("advisor").AtName("model")
	rosterModel := func(i int) path.Path { return path.Root("multiagent").AtName("agents").AtListIndex(i).AtName("model") }
	for name, tc := range map[string]struct {
		plan, prior types.Object // prior null: a create
		want        wantDiag
	}{
		"advisor retired on create": {
			plan: multiagentValue(t, multiagentType20261001, retiredModel),
			want: wantDiag{severity: diag.SeverityError, summary: "Retired Model", path: advisor},
		},
		"advisor retired, kept": {
			plan:  multiagentValue(t, multiagentType20261001, retiredModel),
			prior: multiagentValue(t, multiagentType20261001, retiredModel),
			want: wantDiag{
				severity: diag.SeverityWarning, summary: "Retired Model", path: advisor,
				detail: []string{"this agent's advisor can no longer run"},
			},
		},
		"advisor deprecated": {
			plan: multiagentValue(t, multiagentType20261001, deprecatedModel),
			want: wantDiag{
				severity: diag.SeverityWarning, summary: "Deprecated Model", path: advisor,
				detail: []string{"so this agent's advisor will stop working then"},
			},
		},
		"roster advisor retired on create": {
			plan: multiagentValue(t, multiagentTypeCoordinator, "", "agent:agent_1", "advisor:"+retiredModel),
			want: wantDiag{severity: diag.SeverityError, summary: "Retired Model", path: rosterModel(1)},
		},
		"roster advisor retired, kept at another index": {
			plan:  multiagentValue(t, multiagentTypeCoordinator, "", "agent:agent_1", "advisor:"+retiredModel),
			prior: multiagentValue(t, multiagentTypeCoordinator, "", "advisor:"+retiredModel, "agent:agent_1"),
			want:  wantDiag{severity: diag.SeverityWarning, summary: "Retired Model", path: rosterModel(1)},
		},
		"roster advisor active": {
			plan: multiagentValue(t, multiagentTypeCoordinator, "", "agent:agent_1", "advisor:"+activeAdvisor),
			want: wantDiag{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newModelsServer(t)
			r := lifecycleResource(t, srv, &providerdata.ProviderData{})
			req := resource.ModifyPlanRequest{
				Plan:  schematest.Plan(t, r, map[string]any{"model": activeModel, "name": "a"}),
				State: schematest.NullState(t, r),
			}
			if d := req.Plan.SetAttribute(context.Background(), path.Root("multiagent"), tc.plan); d.HasError() {
				t.Fatal(d)
			}
			if !tc.prior.IsNull() {
				req.State = schematest.State(t, r, map[string]any{"model": activeModel, "name": "a"})
				if d := req.State.SetAttribute(context.Background(), path.Root("multiagent"), tc.prior); d.HasError() {
					t.Fatal(d)
				}
			}
			assertDiag(t, runModifyPlan(r, req), tc.want)
			// The agent's model and the advisor's: one lookup each.
			srv.AssertCalled(modelRoute, 2)
		})
	}
}
