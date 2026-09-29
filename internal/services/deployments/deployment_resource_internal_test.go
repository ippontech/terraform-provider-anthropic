// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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
