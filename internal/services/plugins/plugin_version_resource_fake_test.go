// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

const versionAddr = "anthropic_plugin_version.test"

func versionConfig(pluginID, body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) +
		"resource \"anthropic_plugin_version\" \"test\" {\n  plugin_id = " + hcl(pluginID) + "\n" + body + "}\n"
}

// expectVersion checks a version uploaded from writePluginDir.
func expectVersion(id, pluginID, manifestVersion string, releaseNotes knownvalue.Check) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("plugin_id"), knownvalue.StringExact(pluginID)),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("display_name"), knownvalue.StringExact("Sales Toolkit")),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("description"), knownvalue.StringExact("Account research and call prep.")),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("manifest_version"), knownvalue.StringExact(manifestVersion)),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("release_notes"), releaseNotes),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("reach"), knownvalue.StringExact("contained")),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("content_scan"), knownvalue.ObjectExact(map[string]knownvalue.Check{
			"status": knownvalue.StringExact("completed"), "assessment": knownvalue.StringExact("pass"), "reason": knownvalue.Null(),
		})),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("created_by"), knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("api_actor"), "api_key_id": knownvalue.StringExact(uploaderKeyID),
			"user_id": knownvalue.Null(), "email_address": knownvalue.Null(),
		})),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("source_hash"), knownvalue.StringRegexp(regexp.MustCompile(`^[0-9a-f]{64}$`))),
	}
}

// TestPluginVersionResource_lifecycle uploads a version of a plugin
// uploaded outside Terraform, replaces it with a new version when a file
// changes (source_hash moves at plan), refuses import, uploads again after
// the version vanished, and only forgets the versions on destroy.
func TestPluginVersionResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	pluginID, _ := srv.putOrgPlugin("sales-toolkit")
	dir := writePluginDir(t, "sales-toolkit", "1.5.0")
	srv.setUpload(map[string]any{"manifest_version": "1.5.0"})
	config := versionConfig(pluginID, "  source_dir = "+hcl(dir)+"\n  release_notes = \"Adds the call-prep command.\"\n")
	var firstHash string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if n := srv.versionCount(pluginID); n != 4 {
				return fmt.Errorf("%s holds %d versions after destroy, want 4: a version destroy only forgets it", pluginID, n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(versionAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: append(expectVersion("pluginver_0003", pluginID, "1.5.0", knownvalue.StringExact("Adds the call-prep command.")),
					statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("components"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"type": knownvalue.StringExact("skill"), "name": knownvalue.StringExact("account-research"),
							"description": knownvalue.StringExact("Researches a customer account."),
						}),
					}))),
				Check: func(s *terraform.State) error {
					firstHash = s.RootModule().Resources[versionAddr].Primary.Attributes["source_hash"]
					return nil
				},
			},
			{
				ResourceName:  versionAddr,
				ImportState:   true,
				ImportStateId: "pluginver_0003",
				ExpectError:   regexp.MustCompile(`(?i)resource\s+import\s+not\s+implemented|does\s+not\s+support\s+import`),
			},
			{
				// An edit to a file, with the same configuration, is a new version.
				PreConfig: func() {
					p := filepath.Join(dir, "skills", "account-research", "SKILL.md")
					if err := os.WriteFile(p, []byte("---\nname: account-research\ndescription: Researches an account.\n---\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(versionAddr, plancheck.ResourceActionReplace),
						plancheck.ExpectUnknownValue(versionAddr, tfjsonpath.New("id")),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("id"), knownvalue.StringExact("pluginver_0004")),
				},
				Check: func(s *terraform.State) error {
					if h := s.RootModule().Resources[versionAddr].Primary.Attributes["source_hash"]; h == firstHash {
						return fmt.Errorf("source_hash %s did not change with the content", h)
					}
					return nil
				},
			},
			{
				// A version that answers 404 on refresh is gone: it leaves the
				// state, and the apply uploads a new one.
				PreConfig:        func() { srv.FailNext(routeGetVersion, http.StatusNotFound, 1) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(versionAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("id"), knownvalue.StringExact("pluginver_0005")),
				},
			},
		},
	})
	srv.AssertCalled(routeCreateVersion, 3)
	names, fields := srv.uploadedFiles(routeCreateVersion, 0)
	if want := []string{".claude-plugin/plugin.json", "skills/account-research/SKILL.md"}; !slices.Equal(names, want) {
		t.Errorf("uploaded files = %q, want %q", names, want)
	}
	if fmt.Sprint(fields) != fmt.Sprint(map[string]string{"release_notes": "Adds the call-prep command."}) {
		t.Errorf("upload fields = %v", fields)
	}
	for _, r := range srv.Requests(routeCreateVersion) {
		if r.Path != "/v1/organizations/plugins/"+pluginID+"/versions" {
			t.Errorf("upload path = %q", r.Path)
		}
	}
	srv.AssertCalled(routeDeletePlugin, 0)
	srv.assertRequests()
}

// TestPluginVersionResource_fromArchive uploads a .plugin archive under its
// base name, without release notes: the version declares an MCP server, so
// it reaches remote.
func TestPluginVersionResource_fromArchive(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	pluginID, _ := srv.putOrgPlugin("sales-toolkit")
	archive := writePluginArchive(t, "sales-toolkit", "2.0.0")
	// What the API reads from the archive's manifest and skill.
	srv.setUpload(map[string]any{"display_name": nil, "description": nil, "manifest_version": "2.0.0", "reach": "remote",
		"components": []any{
			map[string]any{"type": "skill", "name": "call-prep", "description": "Prepares a sales call."},
			map[string]any{"type": "mcp_server", "name": "crm", "description": nil},
		}})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:           versionConfig(pluginID, "  source_archive = "+hcl(archive)+"\n"),
			ConfigPlanChecks: providertest.ExpectAction(versionAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("manifest_version"), knownvalue.StringExact("2.0.0")),
				statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("display_name"), knownvalue.Null()),
				statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("release_notes"), knownvalue.Null()),
				statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("reach"), knownvalue.StringExact("remote")),
				statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("components"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"type": knownvalue.StringExact("skill"), "name": knownvalue.StringExact("call-prep"),
						"description": knownvalue.StringExact("Prepares a sales call."),
					}),
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"type": knownvalue.StringExact("mcp_server"), "name": knownvalue.StringExact("crm"), "description": knownvalue.Null(),
					}),
				})),
			},
		}},
	})
	names, fields := srv.uploadedFiles(routeCreateVersion, 0)
	if !slices.Equal(names, []string{"Sales-Toolkit.plugin"}) || len(fields) != 0 {
		t.Errorf("upload = files %q, fields %v; want the archive alone, under its base name", names, fields)
	}
	srv.assertRequests()
}

// TestPluginVersionResource_errors: a manifest whose name is not the
// plugin's is refused by the API; a refresh error that is not a 404 is
// reported; a source missing at apply fails before any upload; no source,
// or both, fail at plan.
func TestPluginVersionResource_errors(t *testing.T) {
	t.Parallel()

	t.Run("other plugin's manifest", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		srv.failNext(routeCreateVersion, http.StatusBadRequest, `the manifest's name "other-plugin" is not the plugin's "sales-toolkit"`, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      versionConfig(pluginID, "  source_dir = "+hcl(writePluginDir(t, "other-plugin", "1.0.0"))+"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+plugin\s+version:\s+API\s+error\s+\(400.*"other-plugin"\s+is\s+not\s+the\s+plugin's`),
			}},
		})
		srv.AssertCalled(routeCreateVersion, 1)
	})

	t.Run("refresh error", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		config := versionConfig(pluginID, "  source_dir = "+hcl(writePluginDir(t, "sales-toolkit", "1.5.0"))+"\n")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:   func() { srv.FailNext(routeGetVersion, http.StatusForbidden, 1) },
					Config:      config,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin\s+version:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
			},
		})
	})

	t.Run("source missing at apply", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		missing := filepath.Join(t.TempDir(), "absent.zip")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      versionConfig(pluginID, "  source_archive = "+hcl(missing)+"\n"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+plugin\s+source.*unable\s+to\s+read\s+source_archive`),
			}},
		})
		srv.AssertCalled(routeCreateVersion, 0)
	})

	for name, tc := range map[string]struct{ body, err string }{
		"no source":    {"  release_notes = \"x\"\n", `(?s)Missing\s+Attribute\s+Configuration.*Exactly\s+one\s+of\s+these\s+attributes`},
		"both sources": {"  source_dir = \"d\"\n  source_archive = \"p.zip\"\n", `Invalid\s+Attribute\s+Combination`},
		"bad plugin":   {"  source_dir = \"d\"\n", `must\s+be\s+a\s+plugin\s+ID`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newPluginsFake(t, 0)
			pluginID := "plugin_0001"
			if name == "bad plugin" {
				pluginID = "pluginver_0001"
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    []resource.TestStep{{Config: versionConfig(pluginID, tc.body), ExpectError: regexp.MustCompile(tc.err)}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
