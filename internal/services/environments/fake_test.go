// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the environments endpoints anthropic_environment uses (beta
// managed-agents-2026-04-01, ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/environments/create,
// .../retrieve, .../update, .../delete and .../archive under the same path.
const (
	routeCreateEnvironment  = "POST /v1/environments"
	routeGetEnvironment     = "GET /v1/environments/{environment_id}"
	routeUpdateEnvironment  = "POST /v1/environments/{environment_id}"
	routeDeleteEnvironment  = "DELETE /v1/environments/{environment_id}"
	routeArchiveEnvironment = "POST /v1/environments/{environment_id}/archive"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allEnvironmentRoutes = []string{
	routeCreateEnvironment, routeGetEnvironment, routeUpdateEnvironment,
	routeDeleteEnvironment, routeArchiveEnvironment,
}

// environmentsFake is the environments API on an sdktest collection. An
// unknown ID answers 404 not_found_error
// (https://platform.claude.com/docs/en/api/errors).
type environmentsFake struct {
	*sdktest.Server
	environments *sdktest.Collection
}

func newEnvironmentsFake(t *testing.T) *environmentsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &environmentsFake{Server: srv, environments: srv.NewCollection("env_")}
	f.Handle(routeCreateEnvironment, f.create)
	f.environments.HandleGet(routeGetEnvironment)
	f.Handle(routeUpdateEnvironment, f.update)
	f.environments.HandleDelete(routeDeleteEnvironment, "environment_deleted")
	f.environments.HandleArchive(routeArchiveEnvironment)
	return f
}

const environmentAddr = "anthropic_environment.test"

// environmentConfig is the provider block for the fake and one anthropic_environment with
// extra appended to its attributes.
func environmentConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_environment" "test" {
  name = "test-environment"
` + extra + `}
`
}

// create stores the environment the body describes. Metadata the body omits
// comes back as {} and config as an unrestricted cloud environment with no
// packages, the defaults of the create reference.
func (f *environmentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	obj := map[string]any{
		"type":        "environment",
		"description": nil,
		"metadata":    map[string]any{},
		"config": map[string]any{
			"type":       "cloud",
			"networking": map[string]any{"type": "unrestricted"},
			"packages": map[string]any{
				"type": "packages", "apt": []any{}, "cargo": []any{}, "gem": []any{},
				"go": []any{}, "npm": []any{}, "pip": []any{},
			},
		},
		"archived_at": nil,
	}
	for k, v := range body {
		if v != nil {
			obj[k] = v
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, f.environments.Create(obj))
}

// update follows https://platform.claude.com/docs/en/api/beta/environments/update:
// an omitted field is kept and a null description clears it; metadata is a
// patch, where a key set to null or "" is deleted and the other stored keys
// are kept; config replaces the stored one as a whole.
func (f *environmentsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("environment_id")
	if !f.environments.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "environment "+id+" not found")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	metadata, patchMetadata := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.environments.Update(id, body, sdktest.Replace)
	if patchMetadata {
		obj, _ = f.environments.Update(id, map[string]any{"metadata": metadata}, sdktest.PatchNullOrEmpty)
	}
	sdktest.WriteJSON(w, http.StatusOK, obj)
}

// assertRequestShape checks what every request of a test must carry: the
// managed-agents beta, the standard key and ?beta=true, on every route called.
func (f *environmentsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allEnvironmentRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertBeta(route, managedAgentsBeta)
		f.AssertCredential(route, sdktest.APIKey)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// checkEnvironmentsDeleted is a CheckDestroy: no environment may be left in
// the fake.
func checkEnvironmentsDeleted(srv *environmentsFake) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if n := srv.environments.Len(); n != 0 {
			return fmt.Errorf("%d environments left after destroy", n)
		}
		return nil
	}
}

// decodeBody reads a JSON object body, numbers kept as json.Number, or
// answers 400 invalid_request_error.
func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object: "+err.Error())
		return nil, false
	}
	return body, true
}
