// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapDeploymentRunToObject_failedRun(t *testing.T) {
	createdAt := time.Date(2026, 5, 9, 0, 0, 1, 0, time.UTC)
	scheduledAt := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)

	run := anthropic.BetaManagedAgentsDeploymentRun{
		ID:           "drun_01abc124",
		DeploymentID: "depl_01xyz",
		CreatedAt:    createdAt,
		Type:         "deployment_run",
		Agent:        anthropic.BetaManagedAgentsAgentReference{ID: "agent_01ghi789", Type: "agent", Version: 3},
		Error: anthropic.BetaManagedAgentsDeploymentRunErrorUnion{
			Type:    "environment_archived_error",
			Message: "environment `env_01abc` is archived",
		},
		TriggerContext: anthropic.BetaManagedAgentsTriggerContextUnion{Type: "schedule", ScheduledAt: scheduledAt},
	}

	value, diags := mapDeploymentRunToObject(run)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	attrs := value.(types.Object).Attributes()

	if got := attrs["id"].(types.String).ValueString(); got != "drun_01abc124" {
		t.Errorf("id = %q, want drun_01abc124", got)
	}
	if got := attrs["deployment_id"].(types.String).ValueString(); got != "depl_01xyz" {
		t.Errorf("deployment_id = %q, want depl_01xyz", got)
	}
	if !attrs["session_id"].(types.String).IsNull() {
		t.Error("session_id should be null on a failed run")
	}
	if got := attrs["created_at"].(types.String).ValueString(); got != "2026-05-09T00:00:01Z" {
		t.Errorf("created_at = %q, want 2026-05-09T00:00:01Z", got)
	}
	if got := attrs["type"].(types.String).ValueString(); got != "deployment_run" {
		t.Errorf("type = %q, want deployment_run", got)
	}

	errObj := attrs["error"].(types.Object)
	if errObj.IsNull() {
		t.Fatal("error should not be null on a failed run")
	}
	if got := errObj.Attributes()["type"].(types.String).ValueString(); got != "environment_archived_error" {
		t.Errorf("error.type = %q, want environment_archived_error", got)
	}
	if got := errObj.Attributes()["message"].(types.String).ValueString(); got != "environment `env_01abc` is archived" {
		t.Errorf("error.message = %q", got)
	}

	trigger := attrs["trigger_context"].(types.Object).Attributes()
	if got := trigger["type"].(types.String).ValueString(); got != "schedule" {
		t.Errorf("trigger_context.type = %q, want schedule", got)
	}
	if got := trigger["scheduled_at"].(types.String).ValueString(); got != "2026-05-09T00:00:00Z" {
		t.Errorf("trigger_context.scheduled_at = %q, want 2026-05-09T00:00:00Z", got)
	}

	agent := attrs["agent"].(types.Object).Attributes()
	if got := agent["id"].(types.String).ValueString(); got != "agent_01ghi789" {
		t.Errorf("agent.id = %q, want agent_01ghi789", got)
	}
	if got := agent["type"].(types.String).ValueString(); got != "agent" {
		t.Errorf("agent.type = %q, want agent", got)
	}
	if got := agent["version"].(types.Int64).ValueInt64(); got != 3 {
		t.Errorf("agent.version = %d, want 3", got)
	}
}

func TestMapDeploymentRunToObject_successfulManualRun(t *testing.T) {
	run := anthropic.BetaManagedAgentsDeploymentRun{
		ID:             "drun_02",
		DeploymentID:   "depl_01xyz",
		CreatedAt:      time.Now(),
		Type:           "deployment_run",
		SessionID:      "sess_01abc",
		Agent:          anthropic.BetaManagedAgentsAgentReference{ID: "agent_01", Type: "agent", Version: 1},
		TriggerContext: anthropic.BetaManagedAgentsTriggerContextUnion{Type: "manual"},
	}

	value, diags := mapDeploymentRunToObject(run)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	attrs := value.(types.Object).Attributes()

	if got := attrs["session_id"].(types.String).ValueString(); got != "sess_01abc" {
		t.Errorf("session_id = %q, want sess_01abc", got)
	}
	if !attrs["error"].(types.Object).IsNull() {
		t.Error("error should be null on a successful run")
	}
	trigger := attrs["trigger_context"].(types.Object).Attributes()
	if got := trigger["type"].(types.String).ValueString(); got != "manual" {
		t.Errorf("trigger_context.type = %q, want manual", got)
	}
	if !trigger["scheduled_at"].(types.String).IsNull() {
		t.Error("trigger_context.scheduled_at should be null on a manual run")
	}
}

func newTestSDKClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test"),
		option.WithMaxRetries(0),
	)
	return &c
}

// TestDeploymentRunsList_paginatesAndForwardsQueryParams drives the exact SDK
// call Read makes against a two-page fake server: every page is fetched, the
// filters are forwarded on the first request and the second carries the
// opaque cursor.
func TestDeploymentRunsList_paginatesAndForwardsQueryParams(t *testing.T) {
	var gotQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query())
		if r.URL.Path != "/v1/deployment_runs" {
			t.Errorf("path = %q, want /v1/deployment_runs", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+sprintfRun("drun_01")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+sprintfRun("drun_02")+`],"next_page":""}`)
	}))
	defer srv.Close()

	params := buildDeploymentRunListParams(DeploymentRunsDataSourceModel{
		DeploymentID: types.StringValue("depl_01xyz"),
		HasError:     types.BoolValue(false),
		TriggerType:  types.StringValue("schedule"),
	})
	pager := newTestSDKClient(t, srv).Beta.DeploymentRuns.ListAutoPaging(context.Background(), params)

	var ids []string
	for pager.Next() {
		ids = append(ids, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if len(ids) != 2 || ids[0] != "drun_01" || ids[1] != "drun_02" {
		t.Fatalf("expected 2 runs across 2 pages, got %v", ids)
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	first := gotQueries[0]
	if got := first.Get("deployment_id"); got != "depl_01xyz" {
		t.Errorf("deployment_id = %q, want depl_01xyz", got)
	}
	if got := first.Get("has_error"); got != "false" {
		t.Errorf("has_error = %q, want false (an explicit false must be sent, not omitted)", got)
	}
	if got := first.Get("trigger_type"); got != "schedule" {
		t.Errorf("trigger_type = %q, want schedule", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

func TestDeploymentRunsList_omitsUnsetFilters(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	params := buildDeploymentRunListParams(DeploymentRunsDataSourceModel{
		DeploymentID: types.StringNull(),
		HasError:     types.BoolNull(),
		TriggerType:  types.StringUnknown(),
	})
	pager := newTestSDKClient(t, srv).Beta.DeploymentRuns.ListAutoPaging(context.Background(), params)

	count := 0
	for pager.Next() {
		count++
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 runs, got %d", count)
	}
	for _, k := range []string{"deployment_id", "has_error", "trigger_type"} {
		if _, present := gotQuery[k]; present {
			t.Errorf("%s sent as %q, want omitted", k, gotQuery.Get(k))
		}
	}
}

func sprintfRun(id string) string {
	return `{"type":"deployment_run","id":"` + id + `","deployment_id":"depl_01xyz",` +
		`"trigger_context":{"type":"schedule","scheduled_at":"2026-05-09T00:00:00Z"},` +
		`"session_id":"sess_01abc","error":null,"agent":{"type":"agent","id":"agent_01","version":1},` +
		`"created_at":"2026-05-09T00:00:01Z"}`
}
