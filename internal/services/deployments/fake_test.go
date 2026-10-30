// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the deployments endpoints (beta managed-agents-2026-04-01,
// ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/deployments/create,
// .../retrieve, .../update, .../archive, .../pause and .../unpause, and
// https://platform.claude.com/docs/en/api/beta/deployment_runs/list.
const (
	routeCreateDeployment  = "POST /v1/deployments"
	routeGetDeployment     = "GET /v1/deployments/{deployment_id}"
	routeUpdateDeployment  = "POST /v1/deployments/{deployment_id}"
	routeArchiveDeployment = "POST /v1/deployments/{deployment_id}/archive"
	routePauseDeployment   = "POST /v1/deployments/{deployment_id}/pause"
	routeUnpauseDeployment = "POST /v1/deployments/{deployment_id}/unpause"
	routeListRuns          = "GET /v1/deployment_runs"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allDeploymentRoutes = []string{
	routeCreateDeployment, routeGetDeployment, routeUpdateDeployment,
	routeArchiveDeployment, routePauseDeployment, routeUnpauseDeployment, routeListRuns,
}

// deploymentsFake is the deployments API on two sdktest collections. An
// unknown ID answers 404 not_found_error
// (https://platform.claude.com/docs/en/api/errors).
type deploymentsFake struct {
	*sdktest.Server
	deployments *sdktest.Collection
	runs        *sdktest.Collection
}

// newDeploymentsFake returns the fake; the runs list answers pages of at most
// pageSize objects (0: the request's limit alone).
func newDeploymentsFake(t *testing.T, pageSize int) *deploymentsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &deploymentsFake{Server: srv, deployments: srv.NewCollection("depl_"), runs: srv.NewCollection("drun_")}
	f.Handle(routeCreateDeployment, f.create)
	f.deployments.HandleGet(routeGetDeployment)
	f.Handle(routeUpdateDeployment, f.update)
	f.deployments.HandleArchive(routeArchiveDeployment)
	// pause and unpause set the status, which mapDeploymentToState maps into
	// status and paused (.../deployments/pause, .../deployments/unpause).
	f.Handle(routePauseDeployment, f.setStatus("paused"))
	f.Handle(routeUnpauseDeployment, f.setStatus("active"))
	// deployment_runs list: deployment_id, has_error ("true for runs with
	// non-null error") and trigger_type filter.
	f.runs.HandleList(routeListRuns, sdktest.PageToken, pageSize, func(r *http.Request, o map[string]any) bool {
		q := r.URL.Query()
		return (!q.Has("deployment_id") || o["deployment_id"] == q.Get("deployment_id")) &&
			(!q.Has("has_error") || fmt.Sprint(o["error"] != nil) == q.Get("has_error")) &&
			(!q.Has("trigger_type") || o["trigger_context"].(map[string]any)["type"] == q.Get("trigger_type"))
	})
	return f
}

// create stores the deployment the body describes, with the fields the
// create reference's response always carries: an active deployment (status,
// which mapDeploymentToState maps into status and paused), no description,
// empty metadata, resources and vault_ids, no schedule, no budget.
func (f *deploymentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	obj := map[string]any{
		"type": "deployment", "status": "active", "archived_at": nil,
		"description": nil, "metadata": map[string]any{}, "resources": []any{}, "vault_ids": []any{},
		"schedule": nil,
	}
	maps.Copy(obj, body)
	normalize(obj)
	sdktest.WriteJSON(w, http.StatusOK, f.deployments.Create(obj))
}

// update follows https://platform.claude.com/docs/en/api/beta/deployments/update:
// an omitted field is kept; description, budget, schedule, resources and
// vault_ids are replaced as a whole, null clearing them; metadata is a
// patch where null deletes a key, which buildMetadataPatch sends for a key
// removed from the configuration.
func (f *deploymentsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deployment_id")
	if !f.deployments.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "deployment "+id+" not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	normalize(body)
	metadata, patch := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.deployments.Update(id, body, sdktest.Replace)
	if patch {
		obj, _ = f.deployments.Update(id, map[string]any{"metadata": metadata}, sdktest.Patch)
	}
	sdktest.WriteJSON(w, http.StatusOK, obj)
}

// normalize turns the agent ID string a request sends ("Accepts the agent ID
// string") into the agent reference object every response carries, whose
// id mapDeploymentToState reads.
func normalize(obj map[string]any) {
	if id, ok := obj["agent"].(string); ok {
		obj["agent"] = map[string]any{"type": "agent", "id": id}
	}
}

// setStatus answers the pause or unpause endpoint.
func (f *deploymentsFake) setStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obj, ok := f.deployments.Update(r.PathValue("deployment_id"), map[string]any{"status": status}, sdktest.Replace)
		if !ok {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "deployment not found")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, obj)
	}
}

// assertRequestShape checks the managed-agents beta and the standard key on
// every route called.
func (f *deploymentsFake) assertRequestShape() {
	f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey, Beta: managedAgentsBeta}, allDeploymentRoutes...)
}

// checkArchived is a CheckDestroy: every deployment of the fake is archived,
// there being no delete endpoint.
func (f *deploymentsFake) checkArchived(*terraform.State) error {
	for _, o := range f.deployments.List(nil) {
		if o["archived_at"] == nil {
			return fmt.Errorf("deployment %s not archived after destroy", o["id"])
		}
	}
	return nil
}

func factories(f *deploymentsFake) map[string]func() (tfprotov6.ProviderServer, error) {
	return providertest.ProviderFactories(f.Server)
}

func config(blocks string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + blocks
}
