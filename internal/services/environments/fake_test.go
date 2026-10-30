// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the environments endpoints the package uses (beta
// managed-agents-2026-04-01, ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/environments/create,
// .../retrieve, .../update, .../delete, .../archive and .../list under the
// same path.
const (
	routeCreateEnvironment  = "POST /v1/environments"
	routeListEnvironments   = "GET /v1/environments"
	routeGetEnvironment     = "GET /v1/environments/{environment_id}"
	routeUpdateEnvironment  = "POST /v1/environments/{environment_id}"
	routeDeleteEnvironment  = "DELETE /v1/environments/{environment_id}"
	routeArchiveEnvironment = "POST /v1/environments/{environment_id}/archive"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allEnvironmentRoutes = []string{
	routeCreateEnvironment, routeListEnvironments, routeGetEnvironment, routeUpdateEnvironment,
	routeDeleteEnvironment, routeArchiveEnvironment,
}

// environmentsFake is the environments API on an sdktest collection. An
// unknown ID answers 404 not_found_error
// (https://platform.claude.com/docs/en/api/errors).
type environmentsFake struct {
	*sdktest.Server
	environments *sdktest.Collection
}

// newEnvironmentsFake returns the fake; the list endpoint answers pages of
// at most pageSize environments (0: the request's limit alone).
func newEnvironmentsFake(t *testing.T, pageSize ...int) *environmentsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &environmentsFake{Server: srv, environments: srv.NewCollection("env_")}
	f.Handle(routeCreateEnvironment, f.create)
	f.environments.HandleGet(routeGetEnvironment)
	f.Handle(routeUpdateEnvironment, f.update)
	f.environments.HandleDelete(routeDeleteEnvironment, "environment_deleted")
	f.environments.HandleArchive(routeArchiveEnvironment)
	size := 0
	if len(pageSize) > 0 {
		size = pageSize[0]
	}
	// https://platform.claude.com/docs/en/api/beta/environments/list: page
	// and next_page cursors; archived environments only with
	// include_archived=true ("default: false").
	f.environments.HandleList(routeListEnvironments, sdktest.PageToken, size, func(r *http.Request, obj map[string]any) bool {
		return r.URL.Query().Get("include_archived") == "true" || obj["archived_at"] == nil
	})
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
// packages, the defaults of the create reference; a config in the body is
// completed as applyConfig says.
func (f *environmentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	obj := map[string]any{
		"type":        "environment",
		"description": nil,
		"metadata":    map[string]any{},
		"archived_at": nil,
	}
	for k, v := range body {
		if v != nil {
			obj[k] = v
		}
	}
	cfg, _ := body["config"].(map[string]any)
	obj["config"] = applyConfig(nil, cfg)
	sdktest.WriteJSON(w, http.StatusOK, f.environments.Create(obj))
}

// update follows https://platform.claude.com/docs/en/api/beta/environments/update:
// an omitted field is kept and a null description clears it; metadata is a
// patch, where a key set to null or "" is deleted and the other stored keys
// are kept; config is merged into the stored one as applyConfig says.
func (f *environmentsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("environment_id")
	stored, found := f.environments.Get(id)
	if !found {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "environment "+id+" not found")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	if cfg, ok := body["config"].(map[string]any); ok {
		prev, _ := stored["config"].(map[string]any)
		body["config"] = applyConfig(prev, cfg)
	}
	metadata, patchMetadata := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.environments.Update(id, body, sdktest.Replace)
	if patchMetadata {
		obj, _ = f.environments.Update(id, map[string]any{"metadata": metadata}, sdktest.PatchNullOrEmpty)
	}
	sdktest.WriteJSON(w, http.StatusOK, obj)
}

// applyConfig returns the cloud config stored after a create (prev nil) or
// an update with body, per BetaCloudConfigParams on the create and update
// references: "on update, omitted fields preserve the existing value", for
// networking, packages and each field of a limited network or of packages.
// The returned BetaCloudConfig always carries every field: a limited network
// its two booleans ("Defaults to false") and allowed_hosts, packages its six
// lists and "type": "packages".
func applyConfig(prev, body map[string]any) map[string]any {
	if prev == nil {
		prev = map[string]any{
			"networking": map[string]any{"type": "unrestricted"},
			"packages":   map[string]any{},
		}
	}
	cfg := map[string]any{"type": "cloud", "networking": prev["networking"]}
	if n, ok := body["networking"].(map[string]any); ok {
		cfg["networking"] = applyNetworking(prev["networking"].(map[string]any), n)
	}
	packages := map[string]any{"type": "packages"}
	for _, k := range []string{"apt", "cargo", "gem", "go", "npm", "pip"} {
		packages[k] = []any{}
		if v, ok := prev["packages"].(map[string]any)[k]; ok {
			packages[k] = v
		}
		if p, ok := body["packages"].(map[string]any); ok && p[k] != nil {
			packages[k] = p[k]
		}
	}
	cfg["packages"] = packages
	return cfg
}

// applyNetworking is applyConfig for the networking member: an unrestricted
// network has no other field; a limited one keeps what the stored limited
// network had for the fields the body omits.
func applyNetworking(prev, body map[string]any) map[string]any {
	if body["type"] == "unrestricted" {
		return map[string]any{"type": "unrestricted"}
	}
	n := map[string]any{"type": "limited", "allow_mcp_servers": false, "allow_package_managers": false, "allowed_hosts": []any{}}
	if prev["type"] == "limited" {
		maps.Copy(n, prev)
	}
	for k, v := range body {
		if v != nil {
			n[k] = v
		}
	}
	return n
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
