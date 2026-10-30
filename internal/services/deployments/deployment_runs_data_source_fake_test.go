// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// seedRun stores a deployment run as the deployment_runs retrieve reference
// shows it (https://platform.claude.com/docs/en/api/beta/deployment_runs/retrieve):
// exactly one of session_id and error is non-null; a scheduled run carries
// scheduled_at, a manual one does not.
func seedRun(f *deploymentsFake, id, deployment string, n int, failed, scheduled bool) {
	o := map[string]any{
		"type": "deployment_run", "id": id, "deployment_id": deployment,
		"agent":      map[string]any{"type": "agent", "id": "agent_1", "version": 4},
		"created_at": at(n), "session_id": "sesn_" + id, "error": nil,
		"trigger_context": map[string]any{"type": "manual"},
	}
	if failed {
		o["session_id"] = nil
		o["error"] = map[string]any{"type": "environment_archived_error", "message": "The environment was archived."}
	}
	if scheduled {
		o["trigger_context"] = map[string]any{"type": "schedule", "scheduled_at": at(n - 1)}
	}
	f.runs.Put(o)
}

// runObject is the exact state value of a seeded run.
func runObject(id, deployment string, n int, failed, scheduled bool) knownvalue.Check {
	session, runError := knownvalue.Check(knownvalue.StringExact("sesn_"+id)), knownvalue.Check(knownvalue.Null())
	if failed {
		session = knownvalue.Null()
		runError = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type":    knownvalue.StringExact("environment_archived_error"),
			"message": knownvalue.StringExact("The environment was archived."),
		})
	}
	trigger, scheduledAt := "manual", knownvalue.Check(knownvalue.Null())
	if scheduled {
		trigger, scheduledAt = "schedule", knownvalue.StringExact(at(n-1))
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "deployment_id": knownvalue.StringExact(deployment),
		"session_id": session, "error": runError,
		"trigger_context": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact(trigger), "scheduled_at": scheduledAt,
		}),
		"agent": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"id": knownvalue.StringExact("agent_1"), "type": knownvalue.StringExact("agent"), "version": knownvalue.Int64Exact(4),
		}),
		"created_at": knownvalue.StringExact(at(n)),
		"type":       knownvalue.StringExact("deployment_run"),
	})
}

// seedRuns stores four runs of two deployments.
func seedRuns(srv *deploymentsFake) {
	seedRun(srv, "drun_1", "depl_1", 2, false, true)
	seedRun(srv, "drun_2", "depl_1", 4, true, true)
	seedRun(srv, "drun_3", "depl_2", 6, false, false)
	seedRun(srv, "drun_4", "depl_1", 8, false, false)
}

const runsAddr = "data.anthropic_deployment_runs.test"

// TestDeploymentRunsDataSource_pages lists every run two per page,
// following next_page, in the API's order.
func TestDeploymentRunsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newDeploymentsFake(t, 2)
	seedRuns(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`data "anthropic_deployment_runs" "test" {}`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(runsAddr, tfjsonpath.New("runs"), knownvalue.ListExact([]knownvalue.Check{
					runObject("drun_1", "depl_1", 2, false, true),
					runObject("drun_2", "depl_1", 4, true, true),
					runObject("drun_3", "depl_2", 6, false, false),
					runObject("drun_4", "depl_1", 8, false, false),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListRuns, "beta=true", "beta=true&page="+sdktest.NextPageToken("drun_2"))
	srv.AssertAllPagesRead(routeListRuns)
	srv.assertRequestShape()
}

// TestDeploymentRunsDataSource_filters sends deployment_id, has_error and
// trigger_type as configured.
func TestDeploymentRunsDataSource_filters(t *testing.T) {
	t.Parallel()
	srv := newDeploymentsFake(t, 2)
	seedRuns(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`
data "anthropic_deployment_runs" "test" {
  deployment_id = "depl_1"
  has_error     = false
  trigger_type  = "manual"
}
`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(runsAddr, tfjsonpath.New("runs"), knownvalue.ListExact([]knownvalue.Check{
					runObject("drun_4", "depl_1", 8, false, false),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListRuns, "beta=true&deployment_id=depl_1&has_error=false&trigger_type=manual")
}

// TestDeploymentRunsDataSource_empty: a deployment without runs is an empty
// list ("Filtering by a non-existent deployment_id returns 200 with empty
// data").
func TestDeploymentRunsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newDeploymentsFake(t, 0)
	seedRuns(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`data "anthropic_deployment_runs" "test" { deployment_id = "depl_none" }`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(runsAddr, tfjsonpath.New("runs"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListRuns, "beta=true&deployment_id=depl_none")
}

// TestDeploymentRunsDataSource_error: an API error is an error diagnostic.
func TestDeploymentRunsDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newDeploymentsFake(t, 0)
	srv.FailNext(routeListRuns, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config:      config(`data "anthropic_deployment_runs" "test" {}`),
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+deployment\s+runs:.*403\s+Forbidden`),
		}},
	})
	srv.AssertCalled(routeListRuns, 1)
}
