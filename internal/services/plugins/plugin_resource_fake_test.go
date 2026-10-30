// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"fmt"
	"net/http"
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

const pluginAddr = "anthropic_plugin.test"

func pluginConfig(body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_plugin\" \"test\" {\n" + body + "}\n"
}

// expectPlugin checks the state of a plugin created from writePluginDir:
// its identity, the served version's display fields, the inherited
// organization-wide setting and the API key that created it.
func expectPlugin(id, servedVersionID string, pinned bool) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("name"), knownvalue.StringExact("sales-toolkit")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("marketplace_id"), knownvalue.StringExact(libraryMarketplaceID)),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_id"), knownvalue.StringExact(servedVersionID)),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_pinned"), knownvalue.Bool(pinned)),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("display_name"), knownvalue.StringExact("Sales Toolkit")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("description"), knownvalue.StringExact("Account research and call prep.")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("manifest_version"), knownvalue.StringExact("1.4.0")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("organization_installation_preference"), knownvalue.StringExact("not_available")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("organization_installation_preference_inherited"), knownvalue.Bool(true)),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("content_scan"), knownvalue.ObjectExact(map[string]knownvalue.Check{
			"status": knownvalue.StringExact("completed"), "assessment": knownvalue.StringExact("pass"), "reason": knownvalue.Null(),
		})),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("components"), knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"type": knownvalue.StringExact("skill"), "name": knownvalue.StringExact("account-research"),
				"description": knownvalue.StringExact("Researches a customer account."),
			}),
		})),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("reach"), knownvalue.StringExact("contained")),
		statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("created_by"), knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("api_actor"), "api_key_id": knownvalue.StringExact(uploaderKeyID),
			"user_id": knownvalue.Null(), "email_address": knownvalue.Null(),
		})),
	}
}

// TestPluginResource_lifecycle uploads a plugin from a directory into the
// library marketplace, imports it, rolls back to the first version after a
// second one was uploaded in claude.ai (which pins the plugin), records a
// change to a create-only input without uploading anything, and creates the
// plugin again after it was deleted behind Terraform's back.
func TestPluginResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	dir := writePluginDir(t, "sales-toolkit", "v1.4.0")
	base := "  source_dir = " + hcl(dir) + "\n"
	pinned := base + "  served_version_id = \"pluginver_0002\"\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if p := srv.plugin("plugin_0004"); p != nil {
				return fmt.Errorf("plugin_0004 still exists after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:           pluginConfig(base + "  release_notes = \"First release\"\n"),
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: append(expectPlugin("plugin_0001", "pluginver_0002", false),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("latest_version_id"), knownvalue.StringExact("pluginver_0002")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("release_notes"), knownvalue.StringExact("First release"))),
			},
			{
				ResourceName:            pluginAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_dir", "release_notes"},
			},
			{
				// A version uploaded in claude.ai is served while the plugin is
				// not pinned; naming the first one rolls back and pins.
				PreConfig:        func() { srv.uploadVersionOutOfBand("plugin_0001", "2.0.0") },
				Config:           pluginConfig(pinned + "  release_notes = \"First release\"\n"),
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: append(expectPlugin("plugin_0001", "pluginver_0002", true),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("latest_version_id"), knownvalue.StringExact("pluginver_0003"))),
			},
			{
				// A create-only input changes in state only.
				Config:           pluginConfig(pinned + "  release_notes = \"Second release\"\n"),
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("release_notes"), knownvalue.StringExact("Second release")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_id"), knownvalue.StringExact("pluginver_0002")),
				},
			},
			{
				PreConfig:         func() { srv.deletePluginOutOfBand("plugin_0001") },
				Config:            pluginConfig(base),
				ConfigPlanChecks:  expectAction(pluginAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectPlugin("plugin_0004", "pluginver_0005", false),
			},
		},
	})
	srv.AssertCalled(routeCreatePlugin, 2)
	names, fields := srv.uploadedFiles(routeCreatePlugin, 0)
	if want := []string{".claude-plugin/plugin.json", "skills/account-research/SKILL.md"}; !slices.Equal(names, want) {
		t.Errorf("uploaded files = %q, want %q", names, want)
	}
	if want := map[string]string{"release_notes": "First release"}; fmt.Sprint(fields) != fmt.Sprint(want) {
		t.Errorf("upload fields = %v, want %v (no marketplace_id: the library marketplace)", fields, want)
	}
	if _, fields := srv.uploadedFiles(routeCreatePlugin, 1); len(fields) != 0 {
		t.Errorf("second upload fields = %v, want none", fields)
	}
	srv.AssertJSONBodies(routeUpdatePlugin, `{"served_version_id": "pluginver_0002"}`)
	srv.AssertCalled(routeDeletePlugin, 1)
	if got := srv.Requests(routeDeletePlugin)[0].Path; got != "/v1/organizations/plugins/plugin_0004" {
		t.Errorf("delete path = %q", got)
	}
	srv.assertRequests()
}

// TestPluginResource_pinWhatIsServed: configuring the version an
// unpinned plugin already serves plans an update that pins it. The API moves
// updated_at on that pin ("including pinning an unpinned Plugin to the
// version it already serves", Update Plugin), so the plan must leave it
// unknown; a plan that kept the prior updated_at made Terraform stop with
// "Provider produced inconsistent result after apply".
func TestPluginResource_pinWhatIsServed(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	base := "  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: pluginConfig(base), ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate)},
			{
				Config:            pluginConfig(base + "  served_version_id = \"pluginver_0002\"\n"),
				ConfigPlanChecks:  expectAction(pluginAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectPlugin("plugin_0001", "pluginver_0002", true),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdatePlugin, `{"served_version_id": "pluginver_0002"}`)
}

// TestPluginResource_intoMarketplaceThenPinned uploads into a named
// marketplace, whose default the plugin inherits; once pinned (a roll back
// to the first version), a version uploaded later is stored without being
// served, and the plan stays empty.
func TestPluginResource_intoMarketplaceThenPinned(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	srv.putMarketplace("marketplace_sales", "sales", "", "manual", "available")
	dir := writePluginDir(t, "sales-toolkit", "1.4.0")
	base := "  source_dir = " + hcl(dir) + "\n  marketplace_id = \"marketplace_sales\"\n"
	pinned := pluginConfig(base + "  served_version_id = \"pluginver_0002\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           pluginConfig(base),
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("marketplace_id"), knownvalue.StringExact("marketplace_sales")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("organization_installation_preference"), knownvalue.StringExact("available")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("organization_installation_preference_inherited"), knownvalue.Bool(true)),
				},
			},
			{
				PreConfig:        func() { srv.uploadVersionOutOfBand("plugin_0001", "v2.0.0") },
				Config:           pinned,
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_id"), knownvalue.StringExact("pluginver_0002")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_pinned"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("manifest_version"), knownvalue.StringExact("1.4.0")),
				},
			},
			{
				PreConfig: func() { srv.uploadVersionOutOfBand("plugin_0001", "3.0.0") },
				Config:    pinned,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(pluginAddr, plancheck.ResourceActionNoop)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_id"), knownvalue.StringExact("pluginver_0002")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("latest_version_id"), knownvalue.StringExact("pluginver_0004")),
				},
			},
		},
	})
	if _, fields := srv.uploadedFiles(routeCreatePlugin, 0); fields["marketplace_id"] != "marketplace_sales" {
		t.Errorf("upload fields = %v, want marketplace_id = marketplace_sales", fields)
	}
	srv.AssertJSONBodies(routeUpdatePlugin, `{"served_version_id": "pluginver_0002"}`)
	srv.assertRequests()
}

// TestPluginResource_deleteToleratesNotFound: a plugin already gone (404)
// when Terraform deletes it is not an error.
func TestPluginResource_deleteToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	config := pluginConfig("  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate)},
			{PreConfig: func() { srv.FailNext(routeDeletePlugin, http.StatusNotFound, 1) }, Config: config, Destroy: true},
		},
	})
	srv.AssertCalled(routeDeletePlugin, 1)
}

// TestPluginResource_servedVersionOnCreate: served_version_id on a new plugin
// fails at plan, before any upload: no version of a plugin about to be
// created exists yet, so there is nothing to serve.
func TestPluginResource_servedVersionOnCreate(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	dir := writePluginDir(t, "sales-toolkit", "1.4.0")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      pluginConfig("  source_dir = " + hcl(dir) + "\n  served_version_id = \"pluginver_0002\"\n"),
			ExpectError: regexp.MustCompile(`served_version_id\s+set\s+on\s+a\s+new\s+plugin`),
		}},
	})
	srv.AssertCalled(routeCreatePlugin, 0)
}

// TestPluginResource_errors: a name taken in the marketplace names the
// plugin holding it; a version whose scan failed is not served; a refresh
// error that is not a 404 is reported; a directory without a manifest fails
// before any upload.
func TestPluginResource_errors(t *testing.T) {
	t.Parallel()

	t.Run("name taken", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		holder, _ := srv.putOrgPlugin("sales-toolkit")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config: pluginConfig("  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+plugin:\s+API\s+error\s+\(409.*already\s+holds\s+a\s+plugin\s+with\s+this\s+name:\s+` +
					holder + `.*terraform\s+import`),
			}},
		})
		srv.AssertCalled(routeCreatePlugin, 1)
	})

	t.Run("scan failed on update", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		base := "  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n"
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: pluginConfig(base), ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate)},
				{
					PreConfig: func() {
						srv.setScan(map[string]any{"status": "completed", "assessment": "fail", "reason": "credential-exposure"})
						srv.uploadVersionOutOfBand("plugin_0001", "2.0.0")
					},
					Config: pluginConfig(base + "  served_version_id = \"pluginver_0003\"\n"),
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+serve\s+plugin\s+version\s+pluginver_0003:.*\(400.*` +
						`content\s+scan\s+failed,\s+so\s+members\s+cannot\s+be\s+served\s+it`),
				},
			},
		})
		srv.AssertCalled(routeUpdatePlugin, 1)
	})

	t.Run("refresh error", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		config := pluginConfig("  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:   func() { srv.FailNext(routeGetPlugin, http.StatusForbidden, 1) },
					Config:      config,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
			},
		})
	})

	t.Run("no manifest", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      pluginConfig("  source_dir = " + hcl(t.TempDir()) + "\n"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+plugin\s+source.*has\s+no\s+\.claude-plugin/plugin\.json`),
			}},
		})
		srv.AssertCalled("", 0)
	})
}

// TestPluginResource_registrationPending: a create answered 503
// registration_pending stored the plugin; the provider does not resend the
// create but uploads the same files as a version, which completes it, and
// reads the plugin back. When that upload fails too, the error says the
// plugin exists and how to adopt it.
func TestPluginResource_registrationPending(t *testing.T) {
	t.Parallel()

	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.answerRegistrationPending()
		dir := writePluginDir(t, "sales-toolkit", "1.4.0")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:           pluginConfig("  source_dir = " + hcl(dir) + "\n  release_notes = \"First release\"\n"),
				ConfigPlanChecks: expectAction(pluginAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("id"), knownvalue.StringExact("plugin_0001")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("served_version_id"), knownvalue.StringExact("pluginver_0003")),
					statecheck.ExpectKnownValue(pluginAddr, tfjsonpath.New("latest_version_id"), knownvalue.StringExact("pluginver_0003")),
				},
			}},
		})
		srv.AssertCalled(routeCreatePlugin, 1)
		srv.AssertCalled(routeCreateVersion, 1)
		created, createFields := srv.uploadedFiles(routeCreatePlugin, 0)
		completed, versionFields := srv.uploadedFiles(routeCreateVersion, 0)
		if !slices.Equal(created, completed) || versionFields["release_notes"] != "First release" || createFields["release_notes"] != "First release" {
			t.Errorf("version upload = %q %v, want the create's files %q and release notes", completed, versionFields, created)
		}
		srv.assertRequests()
	})

	t.Run("completion failed", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.answerRegistrationPending()
		srv.FailNext(routeCreateVersion, http.StatusForbidden, 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config: pluginConfig("  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+plugin:\s+plugin\s+plugin_0001\s+was\s+created\s+but\s+its\s+registration\s+did\s+not\s+complete.*` +
					`API\s+error\s+\(403\s+permission_error\).*Import\s+the\s+plugin\s+with\s+.terraform\s+import.`),
			}},
		})
		srv.AssertCalled(routeCreatePlugin, 1)
		srv.AssertCalled(routeGetPlugin, 0)
	})
}

// TestPluginResource_serveUnknownVersion: a served version the plugin does
// not hold is an error naming it.
func TestPluginResource_serveUnknownVersion(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	base := "  source_dir = " + hcl(writePluginDir(t, "sales-toolkit", "1.4.0")) + "\n"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: pluginConfig(base)},
			{
				Config:      pluginConfig(base + "  served_version_id = \"pluginver_9999\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+serve\s+plugin\s+version\s+pluginver_9999:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdatePlugin, `{"served_version_id": "pluginver_9999"}`)
}

// TestPluginResource_invalidConfig: no source, both sources, an archive of
// another format and a served version that is not a version ID fail at plan,
// before any request.
func TestPluginResource_invalidConfig(t *testing.T) {
	t.Parallel()
	dir := writePluginDir(t, "sales-toolkit", "1.4.0")
	for name, tc := range map[string]struct{ body, err string }{
		"no source":        {"  release_notes = \"x\"\n", `(?s)Missing\s+plugin\s+source.*set\s+.source_dir.\s+or\s+.source_archive`},
		"both sources":     {"  source_dir = " + hcl(dir) + "\n  source_archive = \"p.zip\"\n", `Invalid\s+Attribute\s+Combination`},
		"tarball":          {"  source_archive = \"p.tar.gz\"\n", `must\s+name\s+a\s+\.zip\s+or\s+\.plugin\s+archive`},
		"not a version ID": {"  source_dir = " + hcl(dir) + "\n  served_version_id = \"latest\"\n", `must\s+be\s+a\s+plugin\s+version\s+ID`},
		"empty source_dir": {"  source_dir = \"\"\n", `(?s)source_dir.*at\s+least\s+1`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newPluginsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    []resource.TestStep{{Config: pluginConfig(tc.body), ExpectError: regexp.MustCompile(tc.err)}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
