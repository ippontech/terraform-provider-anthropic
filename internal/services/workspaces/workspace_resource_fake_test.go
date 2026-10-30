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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const workspaceAddr = "anthropic_workspace.test"

func workspaceConfig(body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_workspace\" \"test\" {\n" + body + "}\n"
}

// residencyCheck is the state of data_residency.
func residencyCheck(allowed []string, defaultGeo string) knownvalue.Check {
	geos := make([]knownvalue.Check, len(allowed))
	for i, g := range allowed {
		geos[i] = knownvalue.StringExact(g)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"allowed_inference_geos": knownvalue.ListExact(geos),
		"default_inference_geo":  knownvalue.StringExact(defaultGeo),
		"workspace_geo":          knownvalue.StringExact("us"),
	})
}

func tagsCheck(tags map[string]string) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for k, v := range tags {
		checks[k] = knownvalue.StringExact(v)
	}
	return knownvalue.MapExact(checks)
}

// expectWorkspace is the state of the n-th workspace of the fake, unarchived.
func expectWorkspace(n int, name string, residency, tags, key knownvalue.Check) []statecheck.StateCheck {
	at := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		at("id", knownvalue.StringExact(workspaceID(n))),
		at("name", knownvalue.StringExact(name)),
		at("data_residency", residency),
		at("archived_at", knownvalue.Null()),
		at("created_at", knownvalue.StringExact(fmt.Sprintf("2026-04-%02dT10:00:00Z", n))),
		at("display_color", knownvalue.StringExact("#6C5BB9")),
		at("type", knownvalue.StringExact("workspace")),
		at("tags", tags),
		at("external_key_id", key),
		at("compartment_id", knownvalue.StringExact(fmt.Sprintf("f8a7b6c5-4d3e-4f1a-8b9c-%012d", n))),
		at("user_profile_id", knownvalue.Null()),
		at("inference_data_retention", knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact("disabled")})),
	}
}

var defaultResidency = residencyCheck([]string{"unrestricted"}, "global")

// checkArchived is the CheckDestroy of the workspace tests: destroying
// archives every workspace Terraform created (the API cannot delete one).
func (f *wsFake) checkArchived(ids ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		for _, id := range ids {
			if ws := f.getWS(id); ws == nil || ws["archived_at"] == nil {
				return fmt.Errorf("workspace %s not archived after destroy: %v", id, ws)
			}
		}
		return nil
	}
}

// TestWorkspaceResource_lifecycle creates a workspace from its name alone
// (the API fills data_residency, tags and the computed fields), imports it,
// renames it in place, and archives it on destroy.
func TestWorkspaceResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(1)),
		Steps: []resource.TestStep{
			{
				Config:            workspaceConfig("  name = \"Team\"\n"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(1, "Team", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
			{
				Config:            workspaceConfig("  name = \"Team\"\n"),
				ResourceName:      workspaceAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            workspaceConfig("  name = \"Team (renamed)\"\n"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Team (renamed)", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateWorkspace, `{"name":"Team"}`)
	// The update re-sends the data_residency held in state, without the
	// immutable workspace_geo, and no tags (unchanged).
	srv.AssertJSONBodies(routeUpdateWorkspace,
		`{"name":"Team (renamed)","data_residency":{"allowed_inference_geos":"unrestricted","default_inference_geo":"global"}}`)
	srv.AssertCalled(routeArchiveWorkspace, 1)
	srv.assertAdminRequests(t)
}

// TestWorkspaceResource_tags creates a workspace with tags and updates them
// with the merge patch the API expects: planned keys upserted, a key dropped
// from the configuration sent as null, tags = {} clearing every prior key.
func TestWorkspaceResource_tags(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	config := func(tags string) string { return workspaceConfig("  name = \"Tagged\"\n  tags = " + tags + "\n") }

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(1)),
		Steps: []resource.TestStep{
			{
				Config:            config(`{ env = "prod", team = "platform" }`),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(1, "Tagged", defaultResidency, tagsCheck(map[string]string{"env": "prod", "team": "platform"}), knownvalue.Null()),
			},
			{
				Config:            config(`{ team = "infra", cost = "42" }`),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Tagged", defaultResidency, tagsCheck(map[string]string{"team": "infra", "cost": "42"}), knownvalue.Null()),
			},
			{
				Config:            config(`{}`),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Tagged", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateWorkspace, `{"name":"Tagged","tags":{"env":"prod","team":"platform"}}`)
	const residency = `"data_residency":{"allowed_inference_geos":"unrestricted","default_inference_geo":"global"}`
	srv.AssertJSONBodies(routeUpdateWorkspace,
		`{"name":"Tagged",`+residency+`,"tags":{"env":null,"team":"infra","cost":"42"}}`,
		`{"name":"Tagged",`+residency+`,"tags":{"team":null,"cost":null}}`)
}

// TestWorkspaceResource_unmanagedTags: without tags in the configuration,
// tags set outside Terraform are kept in state and never sent.
func TestWorkspaceResource_unmanagedTags(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           workspaceConfig("  name = \"Free\"\n"),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				Check: func(*terraform.State) error {
					srv.mu.Lock()
					defer srv.mu.Unlock()
					srv.workspace(workspaceID(1))["tags"] = map[string]any{"owner": "console"}
					return nil
				},
			},
			{
				Config:            workspaceConfig("  name = \"Free (2)\"\n"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Free (2)", defaultResidency, tagsCheck(map[string]string{"owner": "console"}), knownvalue.Null()),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateWorkspace,
		`{"name":"Free (2)","data_residency":{"allowed_inference_geos":"unrestricted","default_inference_geo":"global"}}`)
}

// TestWorkspaceResource_dataResidency creates a workspace with a data
// residency (a geo list, sent as an array) and moves it back to
// "unrestricted" in place (sent as the string); workspace_geo is sent on
// create only.
func TestWorkspaceResource_dataResidency(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	config := func(allowed, def string) string {
		return workspaceConfig(fmt.Sprintf("  name = \"Resident\"\n  data_residency = {\n    workspace_geo = \"us\"\n    allowed_inference_geos = %s\n    default_inference_geo = %q\n  }\n", allowed, def))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(1)),
		Steps: []resource.TestStep{
			{
				Config:            config(`["us", "global"]`, "us"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(1, "Resident", residencyCheck([]string{"us", "global"}, "us"), tagsCheck(nil), knownvalue.Null()),
			},
			{
				Config:            config(`["unrestricted"]`, "global"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Resident", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
		},
	})
	srv.AssertJSONBodies(routeCreateWorkspace,
		`{"name":"Resident","data_residency":{"allowed_inference_geos":["us","global"],"default_inference_geo":"us","workspace_geo":"us"}}`)
	srv.AssertJSONBodies(routeUpdateWorkspace,
		`{"name":"Resident","data_residency":{"allowed_inference_geos":"unrestricted","default_inference_geo":"global"}}`)
}

// TestWorkspaceResource_externalKey attaches a first CMEK key in place (the
// update sends external_key_id only while none is attached), then replaces
// the workspace when the attached key changes (write-once).
func TestWorkspaceResource_externalKey(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	config := func(key string) string {
		if key == "" {
			return workspaceConfig("  name = \"Secured\"\n")
		}
		return workspaceConfig("  name = \"Secured\"\n  external_key_id = \"" + key + "\"\n")
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(1), workspaceID(2)),
		Steps: []resource.TestStep{
			{
				Config:            config(""),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(1, "Secured", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
			{
				Config:            config("ekey_first"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectWorkspace(1, "Secured", defaultResidency, tagsCheck(nil), knownvalue.StringExact("ekey_first")),
			},
			{
				// Removing the argument keeps the attached key.
				Config:            config(""),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectWorkspace(1, "Secured", defaultResidency, tagsCheck(nil), knownvalue.StringExact("ekey_first")),
			},
			{
				Config:            config("ekey_second"),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectWorkspace(2, "Secured", defaultResidency, tagsCheck(nil), knownvalue.StringExact("ekey_second")),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateWorkspace,
		`{"name":"Secured","data_residency":{"allowed_inference_geos":"unrestricted","default_inference_geo":"global"},"external_key_id":"ekey_first"}`)
	// The replacement is planned as a create: the computed data_residency
	// and tags are unknown again, so only the configured arguments go out.
	srv.AssertJSONBodies(routeCreateWorkspace, `{"name":"Secured"}`, `{"name":"Secured","external_key_id":"ekey_second"}`)
}

// TestWorkspaceResource_goneOutsideTerraform: a workspace archived or
// deleted behind Terraform's back leaves the state on refresh, and the next
// apply creates a new one.
func TestWorkspaceResource_goneOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)
	config := workspaceConfig("  name = \"Ephemeral\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(3)),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.setArchived(workspaceID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(2, "Ephemeral", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
			{
				PreConfig:         func() { srv.deleteWS(workspaceID(2)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectWorkspace(3, "Ephemeral", defaultResidency, tagsCheck(nil), knownvalue.Null()),
			},
		},
	})
	// Only the last workspace is archived by Terraform.
	if got, want := srv.paths(routeArchiveWorkspace), "/v1/organizations/workspaces/"+workspaceID(3)+"/archive"; got != want {
		t.Errorf("archived %q, want %q", got, want)
	}
}

// TestWorkspaceResource_archiveNotFound: a workspace already gone (404) when
// Terraform archives it is not an error, and the 404 is not retried.
func TestWorkspaceResource_archiveNotFound(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig("  name = \"Racing\"\n"), ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeArchiveWorkspace, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeArchiveWorkspace, 1)
}

// TestWorkspaceResource_apiErrors: an API error on create, refresh, update
// or archive is an error diagnostic naming the operation; the failed POSTs
// are not replayed.
func TestWorkspaceResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(workspaceID(1)),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateWorkspace, http.StatusBadRequest, 1) },
				Config:      workspaceConfig("  name = \"Faulty\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+workspace:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{Config: workspaceConfig("  name = \"Faulty\"\n"), ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetWorkspace, http.StatusForbidden, 1) },
				Config:      workspaceConfig("  name = \"Faulty\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+workspace:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateWorkspace, http.StatusConflict, 1) },
				Config:      workspaceConfig("  name = \"Renamed\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+workspace:\s+API\s+error\s+\(409\s+conflict_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeArchiveWorkspace, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+archive\s+workspace:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeCreateWorkspace, 2)
	srv.AssertCalled(routeUpdateWorkspace, 1)
	// The failed archive, then the final destroy's.
	srv.AssertCalled(routeArchiveWorkspace, 2)
}

// TestWorkspaceResource_reservedTagKey: a tag key starting with "anthropic"
// (any case) fails the plan, before any request.
func TestWorkspaceResource_reservedTagKey(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      workspaceConfig("  name = \"Reserved\"\n  tags = { AnthropicTeam = \"x\" }\n"),
			ExpectError: regexp.MustCompile(`(?s)Invalid\s+tag\s+key.*"AnthropicTeam"\s+must\s+not\s+begin\s+with\s+"anthropic"`),
		}},
	})
	srv.AssertCalled("", 0)
}
