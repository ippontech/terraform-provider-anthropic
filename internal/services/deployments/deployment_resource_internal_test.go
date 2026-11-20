// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// --- unmarshalInitialEvents / unmarshalResourcesNew / unmarshalResourcesUpdate / unmarshalBudget ---

func TestUnmarshalInitialEvents_UserMessage(t *testing.T) {
	raw := `[{"type":"user.message","content":[{"type":"text","text":"hello"}]}]`
	events, err := unmarshalInitialEvents(jsontypes.NewNormalizedValue(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].OfUserMessage == nil {
		t.Fatal("expected the event to resolve to the user.message variant")
	}
}

func TestUnmarshalInitialEvents_InvalidJSON(t *testing.T) {
	if _, err := unmarshalInitialEvents(jsontypes.NewNormalizedValue("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestUnmarshalResourcesNew_GitHubRepository(t *testing.T) {
	raw := `[{"type":"github_repository","url":"https://github.com/ippontech/terraform-provider-anthropic"}]`
	resources, err := unmarshalResourcesNew(jsontypes.NewNormalizedValue(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 1 || resources[0].OfGitHubRepository == nil {
		t.Fatal("expected the resource to resolve to the github_repository variant")
	}
	if resources[0].OfGitHubRepository.URL != "https://github.com/ippontech/terraform-provider-anthropic" {
		t.Errorf("unexpected URL: %s", resources[0].OfGitHubRepository.URL)
	}
}

func TestUnmarshalResourcesUpdate_MemoryStore(t *testing.T) {
	raw := `[{"type":"memory_store","memory_store_id":"memstore_01ABC"}]`
	resources, err := unmarshalResourcesUpdate(jsontypes.NewNormalizedValue(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 1 || resources[0].OfMemoryStore == nil {
		t.Fatal("expected the resource to resolve to the memory_store variant")
	}
	if resources[0].OfMemoryStore.MemoryStoreID != "memstore_01ABC" {
		t.Errorf("unexpected memory_store_id: %s", resources[0].OfMemoryStore.MemoryStoreID)
	}
}

func TestUnmarshalBudget(t *testing.T) {
	raw := `{"max_list_cost":{"amount":"2500","currency":"USD"},"type":"limit"}`
	budget, err := unmarshalBudget(jsontypes.NewNormalizedValue(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if budget.MaxListCost.Amount != "2500" {
		t.Errorf("expected amount 2500, got %s", budget.MaxListCost.Amount)
	}
	if string(budget.Type) != "limit" {
		t.Errorf("expected type limit, got %s", budget.Type)
	}
}

func TestUnmarshalBudget_InvalidJSON(t *testing.T) {
	if _, err := unmarshalBudget(jsontypes.NewNormalizedValue("{not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

// --- buildMetadataPatch ---

func TestBuildMetadataPatch_UpsertAndDelete(t *testing.T) {
	ctx := context.Background()

	plan, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"team": "platform", "env": "prod"})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics building plan map: %v", diags)
	}
	state, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"env": "prod", "stale": "old"})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics building state map: %v", diags)
	}

	patch, diags := buildMetadataPatch(ctx, plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if patch["team"] != "platform" {
		t.Errorf("expected team=platform in patch, got %v", patch["team"])
	}
	if patch["env"] != "prod" {
		t.Errorf("expected env=prod in patch, got %v", patch["env"])
	}
	if v, ok := patch["stale"]; !ok || v != nil {
		t.Errorf("expected stale key removed from state to be an explicit nil in the patch, got %v (present=%v)", v, ok)
	}
}

func TestBuildMetadataPatch_NullPlanAndState(t *testing.T) {
	ctx := context.Background()

	patch, diags := buildMetadataPatch(ctx, types.MapNull(types.StringType), types.MapNull(types.StringType))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(patch) != 0 {
		t.Errorf("expected an empty patch, got %v", patch)
	}
}

// --- mapDeploymentToState ---

func TestMapDeploymentToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	var deployment anthropic.BetaManagedAgentsDeployment
	if err := json.Unmarshal([]byte(`{
		"id": "depl_01ABC",
		"agent": {"id": "agent_01ABC", "type": "agent", "version": 1},
		"archived_at": null,
		"created_at": "`+createdAt.Format(time.RFC3339Nano)+`",
		"description": "",
		"environment_id": "env_01ABC",
		"initial_events": [],
		"metadata": {},
		"name": "my-deployment",
		"paused_reason": null,
		"resources": [],
		"schedule": {"expression": "", "timezone": "", "type": "cron", "last_run_at": null, "upcoming_runs_at": []},
		"status": "active",
		"type": "deployment",
		"updated_at": "`+updatedAt.Format(time.RFC3339Nano)+`",
		"vault_ids": [],
		"budget": null
	}`), &deployment); err != nil {
		t.Fatalf("unmarshalling fixture: %v", err)
	}

	var data DeploymentResourceModel
	diags := mapDeploymentToState(context.Background(), &deployment, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ID.ValueString() != "depl_01ABC" {
		t.Errorf("expected ID depl_01ABC, got %s", data.ID.ValueString())
	}
	if data.AgentID.ValueString() != "agent_01ABC" {
		t.Errorf("expected AgentID agent_01ABC, got %s", data.AgentID.ValueString())
	}
	if data.Status.ValueString() != "active" {
		t.Errorf("expected Status active, got %s", data.Status.ValueString())
	}
	if data.Paused.ValueBool() {
		t.Error("expected Paused false for an active deployment")
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("expected ArchivedAt null, got %s", data.ArchivedAt.ValueString())
	}
	if !data.InitialEvents.IsNull() {
		t.Errorf("expected InitialEvents null for an empty array, got %s", data.InitialEvents.ValueString())
	}
	if !data.Metadata.IsNull() {
		t.Errorf("expected Metadata null for an empty map, got %v", data.Metadata)
	}
	if !data.Schedule.IsNull() {
		t.Errorf("expected Schedule null when expression is empty, got %v", data.Schedule)
	}
	if !data.Budget.IsNull() {
		t.Errorf("expected Budget null, got %s", data.Budget.ValueString())
	}
}

func TestMapDeploymentToState_PausedStatus(t *testing.T) {
	var deployment anthropic.BetaManagedAgentsDeployment
	if err := json.Unmarshal([]byte(`{
		"id": "depl_01ABC",
		"agent": {"id": "agent_01ABC", "type": "agent", "version": 1},
		"archived_at": null,
		"created_at": "2024-01-15T10:00:00Z",
		"description": "",
		"environment_id": "env_01ABC",
		"initial_events": [],
		"metadata": {},
		"name": "my-deployment",
		"paused_reason": {"type": "manual"},
		"resources": [],
		"schedule": {"expression": "", "timezone": "", "type": "cron", "last_run_at": null, "upcoming_runs_at": []},
		"status": "paused",
		"type": "deployment",
		"updated_at": "2024-01-15T11:00:00Z",
		"vault_ids": [],
		"budget": null
	}`), &deployment); err != nil {
		t.Fatalf("unmarshalling fixture: %v", err)
	}

	var data DeploymentResourceModel
	diags := mapDeploymentToState(context.Background(), &deployment, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Paused.ValueBool() {
		t.Error("expected Paused true for a paused deployment")
	}
}

func TestMapDeploymentToState_ScheduleAndVaultIDsAndInitialEvents(t *testing.T) {
	var deployment anthropic.BetaManagedAgentsDeployment
	if err := json.Unmarshal([]byte(`{
		"id": "depl_01ABC",
		"agent": {"id": "agent_01ABC", "type": "agent", "version": 1},
		"archived_at": null,
		"created_at": "2024-01-15T10:00:00Z",
		"description": "d",
		"environment_id": "env_01ABC",
		"initial_events": [{"type":"user.message","content":[{"type":"text","text":"hi"}]}],
		"metadata": {"team": "platform"},
		"name": "my-deployment",
		"paused_reason": null,
		"resources": [{"type":"file","file_id":"file_01ABC","mount_path":null}],
		"schedule": {"expression": "0 9 * * 1-5", "timezone": "UTC", "type": "cron", "last_run_at": null, "upcoming_runs_at": []},
		"status": "active",
		"type": "deployment",
		"updated_at": "2024-01-15T11:00:00Z",
		"vault_ids": ["vlt_01ABC"],
		"budget": {"max_list_cost": {"amount": "2500", "currency": "USD"}, "type": "limit"}
	}`), &deployment); err != nil {
		t.Fatalf("unmarshalling fixture: %v", err)
	}

	var data DeploymentResourceModel
	diags := mapDeploymentToState(context.Background(), &deployment, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.Schedule.IsNull() {
		t.Fatal("expected Schedule to be non-null")
	}
	var sched DeploymentScheduleModel
	diags = data.Schedule.As(context.Background(), &sched, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics reading schedule: %v", diags)
	}
	if sched.Expression.ValueString() != "0 9 * * 1-5" {
		t.Errorf("expected expression 0 9 * * 1-5, got %s", sched.Expression.ValueString())
	}
	if sched.Timezone.ValueString() != "UTC" {
		t.Errorf("expected timezone UTC, got %s", sched.Timezone.ValueString())
	}

	if data.VaultIDs.IsNull() {
		t.Fatal("expected VaultIDs to be non-null")
	}
	var vaultIDs []string
	diags = data.VaultIDs.ElementsAs(context.Background(), &vaultIDs, false)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics reading vault_ids: %v", diags)
	}
	if len(vaultIDs) != 1 || vaultIDs[0] != "vlt_01ABC" {
		t.Errorf("expected vault_ids [vlt_01ABC], got %v", vaultIDs)
	}

	if data.InitialEvents.IsNull() {
		t.Fatal("expected InitialEvents to be non-null")
	}
	if data.Resources.IsNull() {
		t.Fatal("expected Resources to be non-null")
	}
	if data.Budget.IsNull() {
		t.Fatal("expected Budget to be non-null")
	}
}

// --- read-after-write consistency wait ---

func newStaleThenFreshDeploymentServer(t *testing.T, deploymentID string, before, after time.Time, staleReads int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gets++
		updated := after
		if gets <= staleReads {
			updated = before
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":             deploymentID,
			"type":           "deployment",
			"agent":          map[string]any{"id": "agent_01ABC", "type": "agent", "version": 1},
			"archived_at":    nil,
			"created_at":     before.Format(time.RFC3339Nano),
			"description":    "",
			"environment_id": "env_01ABC",
			"initial_events": []any{},
			"metadata":       map[string]any{},
			"name":           "probe",
			"paused_reason":  nil,
			"resources":      []any{},
			"schedule":       map[string]any{"expression": "", "timezone": "", "type": "cron", "last_run_at": nil, "upcoming_runs_at": []any{}},
			"status":         "active",
			"updated_at":     updated.Format(time.RFC3339Nano),
			"vault_ids":      []any{},
			"budget":         nil,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

func newTestDeploymentClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()

	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	return &c
}

func TestAwaitDeploymentUpdateVisible_pollsUntilFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshDeploymentServer(t, "depl_01ABC", before, after, 3)
	client := newTestDeploymentClient(t, srv)

	awaitDeploymentUpdateVisible(context.Background(), client, "depl_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 4 {
		t.Errorf("Get calls = %d, want 4 (three stale reads then the fresh one)", *gets)
	}
}

func TestAwaitDeploymentUpdateVisible_returnsImmediatelyWhenFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshDeploymentServer(t, "depl_01ABC", before, after, 0)
	client := newTestDeploymentClient(t, srv)

	awaitDeploymentUpdateVisible(context.Background(), client, "depl_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

func TestAwaitDeploymentUpdateVisible_givesUpAtTimeout(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshDeploymentServer(t, "depl_01ABC", before, after, 1_000_000)
	client := newTestDeploymentClient(t, srv)

	start := time.Now()
	awaitDeploymentUpdateVisible(context.Background(), client, "depl_01ABC", after, 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

func newFailingDeploymentServer(t *testing.T, status int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"not found"}}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

func TestAwaitDeploymentUpdateVisible_stopsOnTerminalReadError(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, gets := newFailingDeploymentServer(t, status)
			client := newTestDeploymentClient(t, srv)

			writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

			start := time.Now()
			awaitDeploymentUpdateVisible(context.Background(), client, "depl_01ABC", writtenAt, 10*time.Second, 50*time.Millisecond)

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want an immediate return on a terminal read error", elapsed)
			}
			if *gets != 1 {
				t.Errorf("Get calls = %d, want 1 (no retry on a terminal status)", *gets)
			}
		})
	}
}

func TestAwaitDeploymentUpdateVisible_keepsPollingOnOtherReadErrors(t *testing.T) {
	srv, gets := newFailingDeploymentServer(t, 422)
	client := newTestDeploymentClient(t, srv)

	writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	start := time.Now()
	awaitDeploymentUpdateVisible(context.Background(), client, "depl_01ABC", writtenAt, 120*time.Millisecond, 10*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

// --- repository authorization tokens (write-only) ---

const (
	testRepoURL   = "https://github.com/ippontech/private-repo"
	testRepoToken = "ghp_test_secret_token"
)

// deploymentValue builds a resource object value from null defaults and the
// given overrides.
func deploymentValue(t *testing.T, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	r := &DeploymentResource{}
	vals := schematest.NullValues(t, r)
	for k, v := range overrides {
		vals[k] = v
	}
	return tftypes.NewValue(schematest.ResourceObjectType(t, r), vals)
}

func tokensValue(tokens map[string]string) tftypes.Value {
	m := map[string]tftypes.Value{}
	for k, v := range tokens {
		m[k] = tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, m)
}

func validateDeploymentConfig(t *testing.T, overrides map[string]tftypes.Value) resource.ValidateConfigResponse {
	t.Helper()
	cfg := tfsdk.Config{Raw: deploymentValue(t, overrides), Schema: schematest.ResourceSchema(t, &DeploymentResource{})}
	var resp resource.ValidateConfigResponse
	(&deploymentRepositoryTokenValidator{}).ValidateResource(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
	return resp
}

func TestDeploymentSchema_repositoryTokensAreWriteOnlyAndSensitive(t *testing.T) {
	t.Parallel()

	a, ok := schematest.ResourceSchema(t, &DeploymentResource{}).Attributes["repository_authorization_tokens_wo"].(schema.MapAttribute)
	if !ok {
		t.Fatal("repository_authorization_tokens_wo is not a map attribute")
	}
	if !a.WriteOnly || !a.Sensitive || a.Computed {
		t.Errorf("WriteOnly=%v Sensitive=%v Computed=%v, want true, true, false", a.WriteOnly, a.Sensitive, a.Computed)
	}
}

func TestDeploymentValidator_rejectsTokenInResources(t *testing.T) {
	t.Parallel()

	resp := validateDeploymentConfig(t, map[string]tftypes.Value{
		"resources": tftypes.NewValue(tftypes.String,
			`[{"type":"github_repository","url":"`+testRepoURL+`","authorization_token":"`+testRepoToken+`"}]`),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for authorization_token in resources")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Summary()+d.Detail(), testRepoToken) {
			t.Errorf("diagnostic leaks the token: %s: %s", d.Summary(), d.Detail())
		}
	}
}

func TestDeploymentValidator_rejectsTokenAnywhereInResources(t *testing.T) {
	t.Parallel()

	repo := `{"type":"github_repository","url":"` + testRepoURL + `"`
	for name, raw := range map[string]string{
		"after a non-object element": `[` + repo + `,"authorization_token":"` + testRepoToken + `"},1]`,
		"upper case key":             `[` + repo + `,"AUTHORIZATION_TOKEN":"` + testRepoToken + `"}]`,
		"nested key":                 `[` + repo + `,"checkout":{"type":"branch","name":"main","authorization_token":"` + testRepoToken + `"}}]`,
		"object instead of array":    repo + `,"authorization_token":"` + testRepoToken + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := validateDeploymentConfig(t, map[string]tftypes.Value{"resources": tftypes.NewValue(tftypes.String, raw)})
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected an error for %s", raw)
			}
		})
	}
}

func TestDeploymentValidator_requiresVersionWithTokens(t *testing.T) {
	t.Parallel()

	resp := validateDeploymentConfig(t, map[string]tftypes.Value{
		"resources":                          tftypes.NewValue(tftypes.String, `[{"type":"github_repository","url":"`+testRepoURL+`"}]`),
		"repository_authorization_tokens_wo": tokensValue(map[string]string{testRepoURL: testRepoToken}),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when repository_authorization_tokens_wo_version is missing")
	}
}

func TestDeploymentValidator_rejectsTokenForUnknownRepository(t *testing.T) {
	t.Parallel()

	resp := validateDeploymentConfig(t, map[string]tftypes.Value{
		"resources":                                  tftypes.NewValue(tftypes.String, `[{"type":"github_repository","url":"`+testRepoURL+`"}]`),
		"repository_authorization_tokens_wo":         tokensValue(map[string]string{"https://github.com/ippontech/other": testRepoToken}),
		"repository_authorization_tokens_wo_version": tftypes.NewValue(tftypes.Number, 1),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for a token whose URL matches no github_repository resource")
	}
}

func TestDeploymentValidator_acceptsTokensThroughWriteOnlyAttribute(t *testing.T) {
	t.Parallel()

	for name, resources := range map[string]tftypes.Value{
		"known":   tftypes.NewValue(tftypes.String, `[{"type":"github_repository","url":"`+testRepoURL+`"}]`),
		"unknown": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	} {
		t.Run(name, func(t *testing.T) {
			resp := validateDeploymentConfig(t, map[string]tftypes.Value{
				"resources":                                  resources,
				"repository_authorization_tokens_wo":         tokensValue(map[string]string{testRepoURL: testRepoToken}),
				"repository_authorization_tokens_wo_version": tftypes.NewValue(tftypes.Number, 1),
			})
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
		})
	}
}

func TestBuildDeploymentParams_mergesRepositoryTokens(t *testing.T) {
	t.Parallel()

	resources := jsontypes.NewNormalizedValue(`[` +
		`{"type":"github_repository","url":"` + testRepoURL + `"},` +
		`{"type":"github_repository","url":"https://github.com/ippontech/public-repo"},` +
		`{"type":"memory_store","memory_store_id":"memstore_01ABC"}]`)
	model := DeploymentResourceModel{
		Name:          types.StringValue("d"),
		AgentID:       types.StringValue("agent_01ABC"),
		EnvironmentID: types.StringValue("env_01ABC"),
		InitialEvents: jsontypes.NewNormalizedValue(`[{"type":"user.message","content":[{"type":"text","text":"hi"}]}]`),
		Resources:     resources,
		Metadata:      types.MapNull(types.StringType),
		VaultIDs:      types.ListNull(types.StringType),
		Schedule:      types.ObjectNull(deploymentScheduleAttrTypes),
	}
	tokens := map[string]string{testRepoURL: testRepoToken}

	check := func(t *testing.T, body []byte) {
		t.Helper()
		var sent struct {
			Resources []map[string]any `json:"resources"`
		}
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatalf("decoding params: %v", err)
		}
		if len(sent.Resources) != 3 {
			t.Fatalf("resources sent = %d, want 3", len(sent.Resources))
		}
		if got := sent.Resources[0]["authorization_token"]; got != testRepoToken {
			t.Errorf("private repository token = %v, want the configured token", got)
		}
		for _, r := range sent.Resources[1:] {
			if _, ok := r["authorization_token"]; ok {
				t.Errorf("unexpected authorization_token on %v", r)
			}
		}
	}

	t.Run("create", func(t *testing.T) {
		params, diags := buildDeploymentCreateParams(context.Background(), &model, tokens)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		check(t, body)
	})

	t.Run("update", func(t *testing.T) {
		state := model
		params, diags := buildDeploymentUpdateParams(context.Background(), &model, &state, tokens)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		check(t, body)
	})
}

// TestDeploymentResource_createSendsTokenButKeepsItOutOfState runs Create
// against a fake API that, like the real one, does not return the token: the
// token reaches the request body, and the state holds neither the token nor
// a resources value that differs from the configured one.
func TestDeploymentResource_createSendsTokenButKeepsItOutOfState(t *testing.T) {
	t.Parallel()

	resourcesJSON := `[{"type":"github_repository","url":"` + testRepoURL + `"}]`
	var sentBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		sentBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "depl_01ABC", "type": "deployment",
			"agent": {"id": "agent_01ABC", "type": "agent", "version": 1},
			"archived_at": null, "created_at": "2024-01-15T10:00:00Z", "updated_at": "2024-01-15T10:00:00Z",
			"description": "", "environment_id": "env_01ABC",
			"initial_events": [{"type":"user.message","content":[{"type":"text","text":"hi"}]}],
			"metadata": {}, "name": "d", "paused_reason": null,
			"resources": ` + resourcesJSON + `,
			"schedule": {"expression": "", "timezone": "", "type": "cron", "last_run_at": null, "upcoming_runs_at": []},
			"status": "active", "vault_ids": [], "budget": null
		}`))
	}))
	t.Cleanup(srv.Close)

	r := &DeploymentResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
	sch := schematest.ResourceSchema(t, r)

	common := map[string]tftypes.Value{
		"name":           tftypes.NewValue(tftypes.String, "d"),
		"agent_id":       tftypes.NewValue(tftypes.String, "agent_01ABC"),
		"environment_id": tftypes.NewValue(tftypes.String, "env_01ABC"),
		"initial_events": tftypes.NewValue(tftypes.String, `[{"type":"user.message","content":[{"type":"text","text":"hi"}]}]`),
		"resources":      tftypes.NewValue(tftypes.String, resourcesJSON),
		"repository_authorization_tokens_wo_version": tftypes.NewValue(tftypes.Number, 1),
	}
	cfgVals := map[string]tftypes.Value{"repository_authorization_tokens_wo": tokensValue(map[string]string{testRepoURL: testRepoToken})}
	planVals := map[string]tftypes.Value{
		"paused":      tftypes.NewValue(tftypes.Bool, false),
		"id":          tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"status":      tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"created_at":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"updated_at":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"archived_at": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	}
	for k, v := range common {
		cfgVals[k] = v
		planVals[k] = v
	}

	req := resource.CreateRequest{
		Config: tfsdk.Config{Raw: deploymentValue(t, cfgVals), Schema: sch},
		Plan:   tfsdk.Plan{Raw: deploymentValue(t, planVals), Schema: sch},
	}
	resp := resource.CreateResponse{State: schematest.NullState(t, r)}
	r.Create(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if !strings.Contains(string(sentBody), `"authorization_token":"`+testRepoToken+`"`) {
		t.Errorf("request body does not carry the token: %s", sentBody)
	}

	var got DeploymentResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if !got.RepositoryTokens.IsNull() {
		t.Error("write-only tokens reached the state")
	}
	if strings.Contains(got.Resources.ValueString(), testRepoToken) {
		t.Error("the token reached the resources attribute in state")
	}
	if eq, _ := got.Resources.StringSemanticEquals(context.Background(), jsontypes.NewNormalizedValue(resourcesJSON)); !eq {
		t.Errorf("resources in state = %s, want the configured %s", got.Resources.ValueString(), resourcesJSON)
	}
}

func TestBuildDeploymentParams_rejectsTokenWithoutRepository(t *testing.T) {
	t.Parallel()

	base := DeploymentResourceModel{
		Name:          types.StringValue("d"),
		AgentID:       types.StringValue("agent_01ABC"),
		EnvironmentID: types.StringValue("env_01ABC"),
		InitialEvents: jsontypes.NewNormalizedValue(`[{"type":"user.message","content":[{"type":"text","text":"hi"}]}]`),
		Metadata:      types.MapNull(types.StringType),
		VaultIDs:      types.ListNull(types.StringType),
		Schedule:      types.ObjectNull(deploymentScheduleAttrTypes),
	}
	tokens := map[string]string{testRepoURL: testRepoToken}

	for name, resources := range map[string]jsontypes.Normalized{
		"other repository": jsontypes.NewNormalizedValue(`[{"type":"github_repository","url":"https://github.com/ippontech/other"}]`),
		"null resources":   jsontypes.NewNormalizedNull(),
	} {
		t.Run(name, func(t *testing.T) {
			model := base
			model.Resources = resources

			_, createDiags := buildDeploymentCreateParams(context.Background(), &model, tokens)
			state := model
			_, updateDiags := buildDeploymentUpdateParams(context.Background(), &model, &state, tokens)
			for kind, diags := range map[string]diag.Diagnostics{"create": createDiags, "update": updateDiags} {
				if !diags.HasError() {
					t.Errorf("%s: expected an error for a token that matches no repository", kind)
				}
				for _, d := range diags {
					if strings.Contains(d.Summary()+d.Detail(), testRepoToken) {
						t.Errorf("%s: diagnostic leaks the token: %s", kind, d.Detail())
					}
				}
			}
		})
	}
}

// TestDeploymentResource_updateResendsTokenOnVersionChange runs Update through
// the resource method for a change of the token version alone: the token,
// read from the configuration, goes out with resources again.
func TestDeploymentResource_updateResendsTokenOnVersionChange(t *testing.T) {
	t.Parallel()

	resourcesJSON := `[{"type":"github_repository","url":"` + testRepoURL + `"}]`
	deploymentJSON := `{
		"id": "depl_01ABC", "type": "deployment",
		"agent": {"id": "agent_01ABC", "type": "agent", "version": 1},
		"archived_at": null, "created_at": "2024-01-15T10:00:00Z", "updated_at": "2024-01-15T11:00:00Z",
		"description": "", "environment_id": "env_01ABC",
		"initial_events": [{"type":"user.message","content":[{"type":"text","text":"hi"}]}],
		"metadata": {}, "name": "d", "paused_reason": null,
		"resources": ` + resourcesJSON + `,
		"schedule": {"expression": "", "timezone": "", "type": "cron", "last_run_at": null, "upcoming_runs_at": []},
		"status": "active", "vault_ids": [], "budget": null
	}`
	var sentBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/depl_01ABC" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			sentBody, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(deploymentJSON))
	}))
	t.Cleanup(srv.Close)

	r := &DeploymentResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
	sch := schematest.ResourceSchema(t, r)

	values := func(version int, extra map[string]tftypes.Value) tftypes.Value {
		v := map[string]tftypes.Value{
			"name":           tftypes.NewValue(tftypes.String, "d"),
			"agent_id":       tftypes.NewValue(tftypes.String, "agent_01ABC"),
			"environment_id": tftypes.NewValue(tftypes.String, "env_01ABC"),
			"initial_events": tftypes.NewValue(tftypes.String, `[{"type":"user.message","content":[{"type":"text","text":"hi"}]}]`),
			"resources":      tftypes.NewValue(tftypes.String, resourcesJSON),
			"repository_authorization_tokens_wo_version": tftypes.NewValue(tftypes.Number, version),
		}
		for k, val := range extra {
			v[k] = val
		}
		return deploymentValue(t, v)
	}
	computed := map[string]tftypes.Value{
		"id":          tftypes.NewValue(tftypes.String, "depl_01ABC"),
		"paused":      tftypes.NewValue(tftypes.Bool, false),
		"status":      tftypes.NewValue(tftypes.String, "active"),
		"created_at":  tftypes.NewValue(tftypes.String, "2024-01-15T10:00:00Z"),
		"updated_at":  tftypes.NewValue(tftypes.String, "2024-01-15T10:00:00Z"),
		"archived_at": tftypes.NewValue(tftypes.String, nil),
	}

	req := resource.UpdateRequest{
		Config: tfsdk.Config{Raw: values(2, map[string]tftypes.Value{
			"repository_authorization_tokens_wo": tokensValue(map[string]string{testRepoURL: testRepoToken}),
		}), Schema: sch},
		Plan:  tfsdk.Plan{Raw: values(2, computed), Schema: sch},
		State: tfsdk.State{Raw: values(1, computed), Schema: sch},
	}
	resp := resource.UpdateResponse{State: req.State}
	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	if !strings.Contains(string(sentBody), `"authorization_token":"`+testRepoToken+`"`) {
		t.Errorf("update body does not carry the token: %s", sentBody)
	}
	var got DeploymentResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if !got.RepositoryTokens.IsNull() || strings.Contains(got.Resources.ValueString(), testRepoToken) {
		t.Error("the token reached the state")
	}
	if got.RepositoryTokensVersion.ValueInt64() != 2 {
		t.Errorf("token version in state = %v, want 2", got.RepositoryTokensVersion)
	}
}
