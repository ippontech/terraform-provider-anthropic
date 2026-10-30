// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

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

const (
	settingAddr     = "anthropic_plugin_installation_setting.test"
	marketplaceAddr = "anthropic_plugin_marketplace_settings.test"
)

func settingConfig(pluginID, target, preference string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(
		"resource \"anthropic_plugin_installation_setting\" \"test\" {\n  plugin_id = %q\n  target = %q\n  installation_preference = %q\n}\n",
		pluginID, target, preference)
}

func expectSetting(pluginID, target, targetType, preference string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("id"), knownvalue.StringExact(pluginID+":"+target)),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("plugin_id"), knownvalue.StringExact(pluginID)),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("target"), knownvalue.StringExact(target)),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("target_type"), knownvalue.StringExact(targetType)),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("installation_preference"), knownvalue.StringExact(preference)),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("created_at"), knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`))),
		statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("updated_at"), knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`))),
	}
}

// TestPluginInstallationSettingResource_lifecycle sets a plugin's
// organization-wide setting, imports it as <plugin_id>:organization, changes
// it in place, sets it again after it was removed behind Terraform's back,
// and removes it on destroy, which returns the plugin to its marketplace's
// default.
func TestPluginInstallationSettingResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	pluginID, _ := srv.putOrgPlugin("sales-toolkit")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if s := srv.setting(pluginID, "organization"); s != nil {
				return fmt.Errorf("the organization setting %v is still there after destroy", s["installation_preference"])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            settingConfig(pluginID, "organization", "required"),
				ConfigPlanChecks:  providertest.ExpectAction(settingAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSetting(pluginID, "organization", "organization", "required"),
			},
			{ResourceName: settingAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:            settingConfig(pluginID, "organization", "available"),
				ConfigPlanChecks:  providertest.ExpectAction(settingAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSetting(pluginID, "organization", "organization", "available"),
			},
			{
				PreConfig:         func() { srv.removeSettingOutOfBand(pluginID, "organization") },
				Config:            settingConfig(pluginID, "organization", "available"),
				ConfigPlanChecks:  providertest.ExpectAction(settingAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSetting(pluginID, "organization", "organization", "available"),
			},
		},
	})
	srv.AssertJSONBodies(routeSetSetting,
		`{"installation_preference": "required"}`, `{"installation_preference": "available"}`, `{"installation_preference": "available"}`)
	srv.AssertCalled(routeRemoveSetting, 1)
	for _, r := range append(srv.Requests(routeSetSetting), srv.Requests(routeRemoveSetting)...) {
		if r.Path != "/v1/organizations/plugins/"+pluginID+"/installation_settings/organization" {
			t.Errorf("%s path = %q", r.Method, r.Path)
		}
	}
	for _, r := range srv.Requests(routeListSettings) {
		if r.Query.Get("target_type") != "organization" || r.Query.Get("limit") != "100" {
			t.Errorf("settings list query = %q, want target_type=organization and limit=100", r.Query.Encode())
		}
	}
	srv.assertRequests()
}

// TestPluginInstallationSettingResource_groupAmongPages: a group's setting
// is found by paging the plugin's group settings, newest first, past the
// other groups'.
func TestPluginInstallationSettingResource_groupAmongPages(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 1)
	pluginID, _ := srv.putOrgPlugin("sales-toolkit")
	srv.putSetting(pluginID, "organization", "available")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           settingConfig(pluginID, "rbac_group_sales", "required"),
				ConfigPlanChecks: providertest.ExpectAction(settingAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: append(expectSetting(pluginID, "rbac_group_sales", "rbac_group", "required"),
					statecheck.ExpectKnownValue(settingAddr, tfjsonpath.New("id"), knownvalue.StringExact(pluginID+":rbac_group_sales"))),
			},
			{
				// Two groups set later in claude.ai come first in the list.
				PreConfig: func() {
					srv.putSetting(pluginID, "rbac_group_eng", "not_available")
					srv.putSetting(pluginID, "rbac_group_ops", "auto_install")
				},
				Config:            settingConfig(pluginID, "rbac_group_sales", "required"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: expectSetting(pluginID, "rbac_group_sales", "rbac_group", "required"),
			},
		},
	})
	// Longest walk first: a read stops paging once it finds the group.
	srv.assertWalks(routeListSettings, "",
		walk([]string{sdktest.NextPageToken("rbac_group_ops"), sdktest.NextPageToken("rbac_group_eng")}, "limit", "100", "target_type", "rbac_group"),
		walk(nil, "limit", "100", "target_type", "rbac_group"))
	srv.assertRequests()
}

// TestPluginInstallationSettingResource_notFoundAndErrors: a removal that
// answers 404 is success; the plugin deleted behind Terraform's back takes
// its setting with it; refresh and removal errors that are not a 404 are
// reported; a member-owned plugin has no installation setting.
func TestPluginInstallationSettingResource_notFoundAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("removal 404", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		config := settingConfig(pluginID, "organization", "required")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { srv.FailNext(routeRemoveSetting, http.StatusNotFound, 1) }, Config: config, Destroy: true},
			},
		})
		srv.AssertCalled(routeRemoveSetting, 1)
	})

	t.Run("plugin deleted", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		config := settingConfig(pluginID, "organization", "required")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:          func() { srv.deletePluginOutOfBand(pluginID) },
					Config:             config,
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(settingAddr, plancheck.ResourceActionCreate)},
					},
				},
			},
		})
		// The plan-only step does not save the refreshed state, so the final
		// destroy, which does not refresh, removes the setting of a plugin
		// that is gone: the API's 404 is success.
		srv.AssertCalled(routeRemoveSetting, 1)
	})

	t.Run("refresh and removal errors", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		config := settingConfig(pluginID, "organization", "required")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:   func() { srv.FailNext(routeListSettings, http.StatusForbidden, 1) },
					Config:      config,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin\s+installation\s+setting:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
				{
					PreConfig:   func() { srv.FailNext(routeRemoveSetting, http.StatusForbidden, 1) },
					Config:      config,
					Destroy:     true,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+remove\s+plugin\s+installation\s+setting:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
			},
		})
		srv.AssertCalled(routeRemoveSetting, 2)
	})

	t.Run("member-owned plugin", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.putMemberPlugin("plugin_member", "my-helpers", "user_01")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      settingConfig("plugin_member", "organization", "required"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+plugin\s+installation\s+setting:\s+API\s+error\s+\(404\s+not_found_error\)`),
			}},
		})
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		pluginID, _ := srv.putOrgPlugin("sales-toolkit")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: settingConfig(pluginID, "organization", "required")},
				{
					PreConfig:   func() { srv.FailNext(routeSetSetting, http.StatusForbidden, 1) },
					Config:      settingConfig(pluginID, "organization", "available"),
					ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+plugin\s+installation\s+setting:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
			},
		})
	})
}

// TestPluginInstallationSettingResource_invalid: a malformed import ID and
// invalid inputs fail before any request.
func TestPluginInstallationSettingResource_invalid(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ config, err string }{
		"target":     {settingConfig("plugin_01", "group_sales", "required"), `must\s+be\s+.organization.\s+or\s+an\s+RBAC\s+group\s+ID`},
		"preference": {settingConfig("plugin_01", "organization", "always"), `(?s)installation_preference.*value\s+must\s+be\s+one\s+of`},
		"plugin":     {settingConfig("marketplace_01", "organization", "required"), `must\s+be\s+a\s+plugin\s+ID`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newPluginsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    []resource.TestStep{{Config: tc.config, ExpectError: regexp.MustCompile(tc.err)}},
			})
			srv.AssertCalled("", 0)
		})
	}

	t.Run("import ID", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:        settingConfig("plugin_01", "organization", "required"),
				ResourceName:  settingAddr,
				ImportState:   true,
				ImportStateId: "plugin_01",
				ExpectError:   regexp.MustCompile(`(?s)Invalid\s+import\s+ID.*Expected\s+.<plugin_id>:<target>.`),
			}},
		})
		srv.AssertCalled("", 0)
	})
}

func marketplaceSettingsConfig(marketplaceID, preference string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(
		"resource \"anthropic_plugin_marketplace_settings\" \"test\" {\n  marketplace_id = %q\n  default_installation_preference = %q\n}\n",
		marketplaceID, preference)
}

func expectMarketplaceSettings(preference string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(marketplaceAddr, tfjsonpath.New("id"), knownvalue.StringExact("marketplace_sales")),
		statecheck.ExpectKnownValue(marketplaceAddr, tfjsonpath.New("marketplace_id"), knownvalue.StringExact("marketplace_sales")),
		statecheck.ExpectKnownValue(marketplaceAddr, tfjsonpath.New("default_installation_preference"), knownvalue.StringExact(preference)),
		statecheck.ExpectKnownValue(marketplaceAddr, tfjsonpath.New("name"), knownvalue.StringExact("sales")),
		statecheck.ExpectKnownValue(marketplaceAddr, tfjsonpath.New("source"), knownvalue.StringExact("github")),
	}
}

// TestPluginMarketplaceSettingsResource_lifecycle adopts an organization
// marketplace and sets its default, imports it by ID, changes the default
// in place, follows a change made in claude.ai, and leaves the default as it
// is on destroy.
func TestPluginMarketplaceSettingsResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	srv.putMarketplace("marketplace_sales", "sales", "", "github", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if got := srv.marketplaceDefault("marketplace_sales"); got != "required" {
				return fmt.Errorf("default after destroy = %v, want required (left as it was)", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            marketplaceSettingsConfig("marketplace_sales", "available"),
				ConfigPlanChecks:  providertest.ExpectAction(marketplaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMarketplaceSettings("available"),
			},
			{ResourceName: marketplaceAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:            marketplaceSettingsConfig("marketplace_sales", "required"),
				ConfigPlanChecks:  providertest.ExpectAction(marketplaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMarketplaceSettings("required"),
			},
			{
				// A default changed in claude.ai is drift, set back in place.
				PreConfig:         func() { srv.setMarketplaceDefaultOutOfBand("marketplace_sales", "not_available") },
				Config:            marketplaceSettingsConfig("marketplace_sales", "required"),
				ConfigPlanChecks:  providertest.ExpectAction(marketplaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMarketplaceSettings("required"),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateMarketplace,
		`{"default_installation_preference": "available"}`, `{"default_installation_preference": "required"}`,
		`{"default_installation_preference": "required"}`)
	srv.assertRequests()
}

// TestPluginMarketplaceSettingsResource_notFoundAndErrors: a marketplace
// deleted in claude.ai leaves the state; a personal marketplace cannot be
// updated (403); a refresh error that is not a 404 is reported.
func TestPluginMarketplaceSettingsResource_notFoundAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("deleted in claude.ai", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.putMarketplace("marketplace_sales", "sales", "", "github", "")
		config := marketplaceSettingsConfig("marketplace_sales", "available")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:          func() { srv.deleteMarketplaceOutOfBand("marketplace_sales") },
					Config:             config,
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(marketplaceAddr, plancheck.ResourceActionCreate)},
					},
				},
			},
		})
	})

	t.Run("personal marketplace", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.putMarketplace("marketplace_mine", "personal", "user_01", "manual", "")
		// "A member's personal marketplace cannot be updated here (403)."
		srv.FailNext(routeUpdateMarketplace, http.StatusForbidden, 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      marketplaceSettingsConfig("marketplace_mine", "available"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+set\s+the\s+marketplace's\s+default\s+installation\s+setting:\s+API\s+error\s+\(403`),
			}},
		})
	})

	t.Run("refresh and update errors", func(t *testing.T) {
		t.Parallel()
		srv := newPluginsFake(t, 0)
		srv.putMarketplace("marketplace_sales", "sales", "", "github", "")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{
				{Config: marketplaceSettingsConfig("marketplace_sales", "available")},
				{
					PreConfig:   func() { srv.FailNext(routeGetMarketplace, http.StatusForbidden, 1) },
					Config:      marketplaceSettingsConfig("marketplace_sales", "available"),
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin\s+marketplace:\s+API\s+error\s+\(403\s+permission_error\)`),
				},
				{
					PreConfig:   func() { srv.FailNext(routeUpdateMarketplace, http.StatusForbidden, 1) },
					Config:      marketplaceSettingsConfig("marketplace_sales", "required"),
					ExpectError: regexp.MustCompile(`Unable\s+to\s+set\s+the\s+marketplace's\s+default\s+installation\s+setting:\s+API\s+error\s+\(403`),
				},
			},
		})
	})
}
