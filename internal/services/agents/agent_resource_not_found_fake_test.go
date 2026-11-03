// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
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

// agentID is the ID the fake gives to the n-th agent it creates.
func agentID(n int) string { return fmt.Sprintf("agent_%024d", n) }

// expectAgentCreated checks that the step creates the agent and that the
// refresh after the apply plans nothing.
var expectAgentCreated = resource.ConfigPlanChecks{
	PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(agentAddr, plancheck.ResourceActionCreate)},
	PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
}

// An agent deleted outside Terraform is dropped by the refresh and re-created
// from the configuration by the next apply, instead of failing the refresh
// with a 404.
func TestAgentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	config := agentConfig("  description = \"re-created after an out-of-band delete\"\n")
	expectAgent := func(id string) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
			statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("description"), knownvalue.StringExact("re-created after an out-of-band delete")),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: expectAgent(agentID(1)),
			},
			{
				PreConfig: func() {
					if !srv.agents.Delete(agentID(1)) {
						t.Fatalf("%s is not in the fake", agentID(1))
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: expectAgent(agentID(2)),
			},
		},
	})

	// The refresh of step 2 met the 404: two creates, no update, and only the
	// second agent archived by the final destroy.
	srv.AssertCalled(routeCreateAgent, 2)
	srv.AssertCalled(routeUpdateAgent, 0)
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}

// Destroy succeeds when the agent is already gone: the archive answers 404
// although the refresh still found it (deleted between refresh and destroy,
// or destroy run with -refresh=false).
func TestAgentResource_destroyToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentCreated,
			},
			{
				PreConfig: func() { srv.FailNext(routeArchiveAgent, http.StatusNotFound, 1) },
				Config:    agentConfig(""),
				Destroy:   true,
			},
		},
	})

	// One archive, answered 404 and not retried.
	reqs := srv.Requests(routeArchiveAgent)
	if len(reqs) != 1 || reqs[0].Fault != "status 404" || reqs[0].Path != "/v1/agents/"+agentID(1)+"/archive" {
		t.Errorf("archive requests = %+v, want one to %s answered 404", reqs, agentID(1))
	}
	srv.assertRequestShape(t)
}

// singleWorkspaceKey puts srv behind a key scoped to a single workspace: a
// request that carries anthropic-workspace-id, whatever workspace it names,
// is answered 404 "Workspace not found" and never reaches srv (CLAUDE.md,
// "Credentials and clients"); any other request is forwarded. The provider
// talks to the front it returns.
func singleWorkspaceKey(t *testing.T, srv *agentsFake) *sdktest.Server {
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

// keepsAgent checks that the step plans nothing for the agent created first:
// the state still holds it.
var keepsAgent = resource.ConfigPlanChecks{
	PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
}

// A 404 "Workspace not found" says the key cannot use the workspace the
// request names, not that the agent is gone: the refresh fails and the agent
// stays in state, instead of being dropped and created again.
func TestAgentResource_workspaceNotFoundFailsRefresh(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	front := singleWorkspaceKey(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(front),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentCreated,
			},
			{
				Config:      withWorkspaceID(agentConfig("")),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+agent.*Workspace\s+not\s+found`),
			},
			{
				Config:            agentConfig(""),
				ConfigPlanChecks:  keepsAgent,
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("id"), knownvalue.StringExact(agentID(1)))},
			},
		},
	})

	if got := workspace404s(front); len(got) == 0 || got[0] != "GET /v1/agents/"+agentID(1) {
		t.Errorf("workspace 404s = %v, want the refresh of %s", got, agentID(1))
	}
	srv.AssertCalled(routeCreateAgent, 1)
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}

// Destroy without a refresh (terraform destroy -refresh=false) fails on a
// 404 "Workspace not found" and keeps the agent in state: the archive did
// not reach it.
func TestAgentResource_workspaceNotFoundFailsDestroy(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	front := singleWorkspaceKey(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(front),
		AdditionalCLIOptions:     &resource.AdditionalCLIOptions{Plan: resource.PlanOptions{NoRefresh: true}},
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentCreated,
			},
			{
				Config:      withWorkspaceID(agentConfig("")),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+agent.*Workspace\s+not\s+found`),
			},
			{
				Config:            agentConfig(""),
				ConfigPlanChecks:  keepsAgent,
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("id"), knownvalue.StringExact(agentID(1)))},
			},
		},
	})

	if got := workspace404s(front); len(got) != 1 || got[0] != "POST /v1/agents/"+agentID(1)+"/archive" {
		t.Errorf("workspace 404s = %v, want the archive of %s", got, agentID(1))
	}
	// Only the final destroy, without the header, archived the agent.
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}
