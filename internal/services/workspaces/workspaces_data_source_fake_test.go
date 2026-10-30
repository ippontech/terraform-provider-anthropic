// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// richWorkspace is a workspace with every optional field set: a geo list,
// tags, a CMEK key, a user profile and an archive date.
func richWorkspace(n int) map[string]any {
	ws := workspaceJSON(n, fmt.Sprintf("Rich %d", n))
	ws["data_residency"] = map[string]any{"allowed_inference_geos": []string{"us"}, "default_inference_geo": "us", "workspace_geo": "us"}
	ws["tags"] = map[string]any{"team": "data"}
	ws["external_key_id"] = "ekey_rich"
	ws["user_profile_id"] = "uprof_rich"
	ws["archived_at"] = "2026-05-02T00:00:00Z"
	ws["display_color"] = "#00AA00"
	return ws
}

// workspaceObject is the exact list item of workspaceJSON(n, name).
func workspaceObject(n int, name string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":                       knownvalue.StringExact(workspaceID(n)),
		"name":                     knownvalue.StringExact(name),
		"data_residency":           defaultResidency,
		"archived_at":              knownvalue.Null(),
		"created_at":               knownvalue.StringExact(fmt.Sprintf("2026-04-%02dT10:00:00Z", n)),
		"display_color":            knownvalue.StringExact("#6C5BB9"),
		"type":                     knownvalue.StringExact("workspace"),
		"tags":                     tagsCheck(nil),
		"external_key_id":          knownvalue.Null(),
		"compartment_id":           knownvalue.StringExact(fmt.Sprintf("f8a7b6c5-4d3e-4f1a-8b9c-%012d", n)),
		"user_profile_id":          knownvalue.Null(),
		"inference_data_retention": knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact("disabled")}),
	})
}

// richObject is the exact list item of richWorkspace(n).
func richObject(n int) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":                       knownvalue.StringExact(workspaceID(n)),
		"name":                     knownvalue.StringExact(fmt.Sprintf("Rich %d", n)),
		"data_residency":           residencyCheck([]string{"us"}, "us"),
		"archived_at":              knownvalue.StringExact("2026-05-02T00:00:00Z"),
		"created_at":               knownvalue.StringExact(fmt.Sprintf("2026-04-%02dT10:00:00Z", n)),
		"display_color":            knownvalue.StringExact("#00AA00"),
		"type":                     knownvalue.StringExact("workspace"),
		"tags":                     tagsCheck(map[string]string{"team": "data"}),
		"external_key_id":          knownvalue.StringExact("ekey_rich"),
		"compartment_id":           knownvalue.StringExact(fmt.Sprintf("f8a7b6c5-4d3e-4f1a-8b9c-%012d", n)),
		"user_profile_id":          knownvalue.StringExact("uprof_rich"),
		"inference_data_retention": knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact("disabled")}),
	})
}

// TestWorkspacesDataSource_pages lists five workspaces served two per page
// through the real Read: it follows last_id with after_id until has_more is
// false and asks for the largest page; archived workspaces and the Default
// Workspace are left out unless asked for.
func TestWorkspacesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 2)
	for n := 1; n <= 5; n++ {
		srv.putWorkspace(workspaceJSON(n, fmt.Sprintf("WS %d", n)))
	}
	srv.putWorkspace(richWorkspace(6)) // archived
	srv.setDefault(workspaceID(3))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspaces" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspaces.test", tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{
					workspaceObject(1, "WS 1"), workspaceObject(2, "WS 2"), workspaceObject(4, "WS 4"), workspaceObject(5, "WS 5"),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListWorkspaces, "limit=1000", "after_id="+workspaceID(2)+"&limit=1000")
	srv.AssertAllPagesRead(routeListWorkspaces)
	srv.assertAdminRequests(t)
}

// TestWorkspacesDataSource_includeArchivedAndDefault sends both filters on
// every page and maps every optional field.
func TestWorkspacesDataSource_includeArchivedAndDefault(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 2)
	srv.putWorkspace(workspaceJSON(1, "Default"))
	srv.putWorkspace(workspaceJSON(2, "WS 2"))
	srv.putWorkspace(richWorkspace(3))
	srv.setDefault(workspaceID(1))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_workspaces" "test" {
  include_archived = true
  include_default  = true
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspaces.test", tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{
					workspaceObject(1, "Default"), workspaceObject(2, "WS 2"), richObject(3),
				})),
			},
		}},
	})
	const filters = "include_archived=true&include_default=true&limit=1000"
	srv.AssertWalks(routeListWorkspaces, filters, "after_id="+workspaceID(2)+"&"+filters)
	srv.AssertAllPagesRead(routeListWorkspaces)
}

// TestWorkspacesDataSource_falseFiltersNotSent: filters set to false are not
// sent (the API defaults them to false).
func TestWorkspacesDataSource_falseFiltersNotSent(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_workspaces" "test" {
  include_archived = false
  include_default  = false
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspaces.test", tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListWorkspaces, "limit=1000")
}

// TestWorkspacesDataSource_error: an API error is an error diagnostic.
func TestWorkspacesDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	srv.FailNext(routeListWorkspaces, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspaces" "test" {}`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+workspaces:\s+API\s+error\s+\(403\s+permission_error\)`),
		}},
	})
	srv.AssertCalled(routeListWorkspaces, 1)
}

// TestWorkspaceDataSource_byID maps one workspace, every optional field set.
func TestWorkspaceDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	srv.putWorkspace(workspaceJSON(1, "Other"))
	srv.putWorkspace(richWorkspace(2))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspace" "test" { id = "` + workspaceID(2) + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("id"), knownvalue.StringExact(workspaceID(2))),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("name"), knownvalue.StringExact("Rich 2")),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("data_residency"), residencyCheck([]string{"us"}, "us")),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("archived_at"), knownvalue.StringExact("2026-05-02T00:00:00Z")),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("tags"), tagsCheck(map[string]string{"team": "data"})),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("external_key_id"), knownvalue.StringExact("ekey_rich")),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("user_profile_id"), knownvalue.StringExact("uprof_rich")),
				statecheck.ExpectKnownValue("data.anthropic_workspace.test", tfjsonpath.New("display_color"), knownvalue.StringExact("#00AA00")),
			},
		}},
	})
	if got := srv.paths(routeGetWorkspace); got == "" {
		t.Error("the data source never read the workspace")
	}
	for _, r := range srv.Requests(routeGetWorkspace) {
		if r.Path != "/v1/organizations/workspaces/"+workspaceID(2) {
			t.Errorf("read %s, want the configured workspace", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestWorkspaceDataSource_unknownID: an unknown workspace is an error.
func TestWorkspaceDataSource_unknownID(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspace" "test" { id = "wrkspc_missing" }`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+workspace:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeGetWorkspace, 1)
}
