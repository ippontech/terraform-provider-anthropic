// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// environmentID is the ID the fake gives to the n-th environment it creates.
func environmentID(n int) string { return fmt.Sprintf("env_%024d", n) }

// expectEnvironmentCreated checks that the step creates the environment and
// that the refresh after the apply plans nothing.
var expectEnvironmentCreated = resource.ConfigPlanChecks{
	PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(environmentAddr, plancheck.ResourceActionCreate)},
	PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
}

// An environment deleted outside Terraform is dropped by the refresh and
// re-created from the configuration by the next apply, instead of failing the
// refresh with a 404.
func TestEnvironmentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	config := environmentConfig("  description = \"re-created after an out-of-band delete\"\n")
	expectEnvironment := func(id string) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.StringExact("re-created after an out-of-band delete")),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectEnvironmentCreated,
				ConfigStateChecks: expectEnvironment(environmentID(1)),
			},
			{
				PreConfig: func() {
					if !srv.environments.Delete(environmentID(1)) {
						t.Fatalf("%s is not in the fake", environmentID(1))
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectEnvironmentCreated,
				ConfigStateChecks: expectEnvironment(environmentID(2)),
			},
		},
	})

	// The refresh of step 2 met the 404: two creates, no update, and only the
	// second environment deleted by the final destroy.
	srv.AssertCalled(routeCreateEnvironment, 2)
	srv.AssertCalled(routeUpdateEnvironment, 0)
	srv.AssertCalled(routeDeleteEnvironment, 1)
	srv.assertRequestShape(t)
}

// Destroy succeeds when the environment is already gone: here the DELETE and
// the archive both answer 404 although the refresh still found the objects
// (deleted between refresh and destroy, or destroy run with -refresh=false).
func TestEnvironmentResource_destroyToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	config := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_environment" "deleted" {
  name = "deleted-on-destroy"
}

resource "anthropic_environment" "archived" {
  name               = "archived-on-destroy"
  archive_on_destroy = true
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_environment.deleted", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("anthropic_environment.archived", plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				PreConfig: func() {
					srv.FailNext(routeDeleteEnvironment, http.StatusNotFound, 1)
					srv.FailNext(routeArchiveEnvironment, http.StatusNotFound, 1)
				},
				Config:  config,
				Destroy: true,
			},
		},
	})

	// One DELETE and one archive, each answered 404 and not retried, on the
	// environment whose archive_on_destroy selects it.
	ids := map[string]string{}
	for _, id := range []string{environmentID(1), environmentID(2)} {
		obj, _ := srv.environments.Get(id)
		ids[fmt.Sprint(obj["name"])] = id
	}
	for route, want := range map[string]string{
		routeDeleteEnvironment:  "/v1/environments/" + ids["deleted-on-destroy"],
		routeArchiveEnvironment: "/v1/environments/" + ids["archived-on-destroy"] + "/archive",
	} {
		reqs := srv.Requests(route)
		if len(reqs) != 1 || reqs[0].Fault != "status 404" || reqs[0].Path != want {
			t.Errorf("%s: requests = %+v, want one to %s answered 404", route, reqs, want)
		}
	}
	srv.assertRequestShape(t)
}

// singleWorkspaceKey puts srv behind a key scoped to a single workspace: a
// request that carries anthropic-workspace-id, whatever workspace it names,
// is answered 404 "Workspace not found" and never reaches srv (CLAUDE.md,
// "Credentials and clients"); any other request is forwarded. The provider
// talks to the front it returns.
func singleWorkspaceKey(t *testing.T, srv *environmentsFake) *sdktest.Server {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := sdktest.NewServer(t)
	front.Handle("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-workspace-id") != "" {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "Workspace not found")
			return
		}
		proxy.ServeHTTP(w, r)
	})
	return front
}

// workspace404s returns the requests front answered 404 "Workspace not
// found", as "METHOD /path".
func workspace404s(front *sdktest.Server) []string {
	var out []string
	for _, r := range front.Requests("/") {
		if r.Header.Get("anthropic-workspace-id") != "" {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

// withWorkspaceID sets the provider's workspace_id in config, so the
// standard client sends anthropic-workspace-id on every request.
func withWorkspaceID(config string) string {
	const block = "provider \"anthropic\" {\n"
	return strings.Replace(config, block, block+"  workspace_id  = \"wrkspc_01TerraformTests\"\n", 1)
}

// A 404 "Workspace not found" says the key cannot use the workspace the
// request names, not that the environment is gone: the refresh fails and the
// environment stays in state, instead of being dropped and created again.
func TestEnvironmentResource_workspaceNotFoundFailsRefresh(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	front := singleWorkspaceKey(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(front),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:           environmentConfig(""),
				ConfigPlanChecks: expectEnvironmentCreated,
			},
			{
				Config:      withWorkspaceID(environmentConfig("")),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+environment.*Workspace\s+not\s+found`),
			},
			{
				Config:            environmentConfig(""),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("id"), knownvalue.StringExact(environmentID(1)))},
			},
		},
	})

	if got := workspace404s(front); len(got) == 0 || got[0] != "GET /v1/environments/"+environmentID(1) {
		t.Errorf("workspace 404s = %v, want the refresh of %s", got, environmentID(1))
	}
	srv.AssertCalled(routeCreateEnvironment, 1)
	srv.AssertCalled(routeDeleteEnvironment, 1)
	srv.assertRequestShape(t)
}

// Destroy without a refresh (terraform destroy -refresh=false) fails on a
// 404 "Workspace not found", for the DELETE and for the archive of
// archive_on_destroy, and keeps both environments in state: neither call
// reached them.
func TestEnvironmentResource_workspaceNotFoundFailsDestroy(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	front := singleWorkspaceKey(t, srv)
	config := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_environment" "deleted" {
  name = "deleted-on-destroy"
}

resource "anthropic_environment" "archived" {
  name               = "archived-on-destroy"
  archive_on_destroy = true
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(front),
		AdditionalCLIOptions:     &resource.AdditionalCLIOptions{Plan: resource.PlanOptions{NoRefresh: true}},
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_environment.deleted", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("anthropic_environment.archived", plancheck.ResourceActionCreate),
					},
				},
			},
			{
				Config:      withWorkspaceID(config),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+(delete|archive)\s+environment.*Workspace\s+not\s+found`),
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})

	// The header reached neither environment: one DELETE and one archive
	// answered 404 by the front.
	got := workspace404s(front)
	slices.Sort(got)
	if len(got) != 2 || !strings.HasPrefix(got[0], "DELETE /v1/environments/") || !strings.HasPrefix(got[1], "POST /v1/environments/") || !strings.HasSuffix(got[1], "/archive") {
		t.Errorf("workspace 404s = %v, want one DELETE and one archive", got)
	}
	// The final destroy, without the header, deleted one and archived the
	// other.
	srv.AssertCalled(routeDeleteEnvironment, 1)
	srv.AssertCalled(routeArchiveEnvironment, 1)
	left := srv.environments.List(nil)
	if len(left) != 1 || left[0]["name"] != "archived-on-destroy" || left[0]["archived_at"] == nil {
		t.Errorf("environments left = %v, want the archived one only", left)
	}
	srv.assertRequestShape(t)
}
