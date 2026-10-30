// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func dataConfig(typ, body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "data \"" + typ + "\" \"test\" {\n" + body + "}\n"
}

// walk is the queries of one read of a list: the first page, then one per
// cursor, each with beta=true and the extra parameters given.
func walk(cursors []string, kv ...string) []string {
	var out []string
	for _, c := range append([]string{""}, cursors...) {
		q := url.Values{"beta": {"true"}}
		for i := 0; i+1 < len(kv); i += 2 {
			q.Add(kv[i], kv[i+1])
		}
		if c != "" {
			q.Set("page", c)
		}
		out = append(out, q.Encode())
	}
	return out
}

// orgPluginCheck is the list element, or the data source state, of a plugin
// seeded by putOrgPlugin: organization-owned, unpinned, never set up beyond
// its marketplace's default.
func orgPluginCheck(id, name, versionID string) map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "name": knownvalue.StringExact(name),
		"display_name": knownvalue.StringExact("Seeded"), "description": knownvalue.Null(), "manifest_version": knownvalue.StringExact("1.0.0"),
		"served_version_id": knownvalue.StringExact(versionID), "served_version_pinned": knownvalue.Bool(false),
		"latest_version_id": knownvalue.StringExact(versionID), "owner_type": knownvalue.StringExact("organization"),
		"owner_user_id": knownvalue.Null(), "marketplace_id": knownvalue.StringExact(libraryMarketplaceID),
		"created_by": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("api_actor"), "api_key_id": knownvalue.StringExact(uploaderKeyID),
			"user_id": knownvalue.Null(), "email_address": knownvalue.Null(),
		}),
		"organization_installation_preference":           knownvalue.StringExact("not_available"),
		"organization_installation_preference_inherited": knownvalue.Bool(true),
		"content_scan": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"status": knownvalue.StringExact("completed"), "assessment": knownvalue.StringExact("pass"), "reason": knownvalue.Null(),
		}),
		"components": knownvalue.ListExact([]knownvalue.Check{}), "reach": knownvalue.StringExact("contained"),
		"created_at": knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`)),
		"updated_at": knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`)),
	}
}

// memberPluginCheck is the state of a plugin seeded by putMemberPlugin: a
// member's, with no organization-wide setting.
func memberPluginCheck(id, name, userID, versionID string) map[string]knownvalue.Check {
	c := orgPluginCheck(id, name, versionID)
	c["display_name"], c["manifest_version"] = knownvalue.Null(), knownvalue.Null()
	c["owner_type"], c["owner_user_id"] = knownvalue.StringExact("user"), knownvalue.StringExact(userID)
	c["marketplace_id"] = knownvalue.StringExact("marketplace_" + userID)
	c["created_by"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type": knownvalue.StringExact("user_actor"), "user_id": knownvalue.StringExact(userID),
		"email_address": knownvalue.Null(), "api_key_id": knownvalue.Null(),
	})
	c["organization_installation_preference"] = knownvalue.Null()
	c["organization_installation_preference_inherited"] = knownvalue.Null()
	return c
}

func attrChecks(addr string, checks map[string]knownvalue.Check) []statecheck.StateCheck {
	var out []statecheck.StateCheck
	for k, c := range checks {
		out = append(out, statecheck.ExpectKnownValue(addr, tfjsonpath.New(k), c))
	}
	return out
}

// TestPluginDataSource_readAndNotFound reads an organization-owned plugin
// and a member's, then fails on a plugin that does not exist.
func TestPluginDataSource_readAndNotFound(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 0)
	orgID, orgVersion := srv.putOrgPlugin("sales-toolkit")
	srv.putMemberPlugin("plugin_member", "my-helpers", "user_01")
	const addr = "data.anthropic_plugin.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:            dataConfig("anthropic_plugin", "  id = "+hcl(orgID)+"\n"),
				ConfigStateChecks: attrChecks(addr, orgPluginCheck(orgID, "sales-toolkit", orgVersion)),
			},
			{
				Config:            dataConfig("anthropic_plugin", "  id = \"plugin_member\"\n"),
				ConfigStateChecks: attrChecks(addr, memberPluginCheck("plugin_member", "my-helpers", "user_01", "pluginver_0003")),
			},
			{
				Config:      dataConfig("anthropic_plugin", "  id = \"plugin_gone\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Plugin\s+Not\s+Found.*No\s+plugin\s+plugin_gone\s+in\s+the\s+organization`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetPlugin, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_plugin", "  id = "+hcl(orgID)+"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.assertRequests()
}

// TestPluginsDataSource_pagesFiltersAndEmpty lists three plugins, newest
// first, two per page; sends each filter; and renders no match as [].
func TestPluginsDataSource_pagesFiltersAndEmpty(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 2)
	a, av := srv.putOrgPlugin("alpha")
	b, bv := srv.putOrgPlugin("beta")
	srv.putMemberPlugin("plugin_member", "my-helpers", "user_01")
	const addr = "data.anthropic_plugins.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: dataConfig("anthropic_plugins", ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("plugins"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(memberPluginCheck("plugin_member", "my-helpers", "user_01", "pluginver_0005")),
						knownvalue.ObjectExact(orgPluginCheck(b, "beta", bv)),
						knownvalue.ObjectExact(orgPluginCheck(a, "alpha", av)),
					})),
				},
			},
			{
				Config: dataConfig("anthropic_plugins", "  owner_type = \"organization\"\n  marketplace_id = \""+libraryMarketplaceID+"\"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("plugins"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(orgPluginCheck(b, "beta", bv)),
						knownvalue.ObjectExact(orgPluginCheck(a, "alpha", av)),
					})),
				},
			},
			{
				Config: dataConfig("anthropic_plugins", "  owner_user_id = \"user_02\"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("plugins"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			{
				PreConfig:   func() { srv.FailNext(routeListPlugins, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_plugins", ""),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+plugins:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.assertWalks(routeListPlugins, "",
		walk([]string{pageToken(2)}, "limit", "100"),
		walk(nil, "limit", "100", "marketplace_id", libraryMarketplaceID, "owner_type", "organization"),
		walk(nil, "limit", "100", "owner_user_id", "user_02"))
	srv.assertRequests()
}

// TestPluginVersionsDataSource_pagesAndErrors lists a plugin's three
// versions, newest first, two per page; a plugin that does not exist is an
// error.
func TestPluginVersionsDataSource_pagesAndErrors(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 2)
	pluginID, v1 := srv.putOrgPlugin("sales-toolkit")
	v2 := srv.uploadVersionOutOfBand(pluginID, "v2.0.0")
	srv.setScan(nil)
	v3 := srv.uploadVersionOutOfBand(pluginID, "3.0.0")
	const addr = "data.anthropic_plugin_versions.test"
	version := func(id, manifestVersion, displayName string, scanned bool) knownvalue.Check {
		var scan knownvalue.Check = knownvalue.Null()
		if scanned {
			scan = knownvalue.ObjectExact(map[string]knownvalue.Check{
				"status": knownvalue.StringExact("completed"), "assessment": knownvalue.StringExact("pass"), "reason": knownvalue.Null(),
			})
		}
		return knownvalue.ObjectExact(map[string]knownvalue.Check{
			"id": knownvalue.StringExact(id), "plugin_id": knownvalue.StringExact(pluginID),
			"display_name": knownvalue.StringExact(displayName), "description": knownvalue.Null(),
			"manifest_version": knownvalue.StringExact(manifestVersion), "release_notes": knownvalue.Null(),
			"created_by": knownvalue.ObjectExact(map[string]knownvalue.Check{
				"type": knownvalue.StringExact("api_actor"), "api_key_id": knownvalue.StringExact(uploaderKeyID),
				"user_id": knownvalue.Null(), "email_address": knownvalue.Null(),
			}),
			"content_scan": scan, "components": knownvalue.ListExact([]knownvalue.Check{}), "reach": knownvalue.StringExact("contained"),
			"created_at": knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`)),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: dataConfig("anthropic_plugin_versions", "  plugin_id = "+hcl(pluginID)+"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("versions"), knownvalue.ListExact([]knownvalue.Check{
						version(v3, "3.0.0", "Sales Toolkit", false), version(v2, "2.0.0", "Sales Toolkit", true), version(v1, "1.0.0", "Seeded", true),
					})),
				},
			},
			{
				Config:      dataConfig("anthropic_plugin_versions", "  plugin_id = \"plugin_gone\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+the\s+versions\s+of\s+plugin\s+plugin_gone:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
		},
	})
	srv.assertWalks(routeListVersions, "/v1/organizations/plugins/"+pluginID+"/versions", walk([]string{pageToken(2)}, "limit", "1000"))
	srv.assertRequests()
}

func marketplaceCheck(id, name, userID, source string, defaultPreference any) map[string]knownvalue.Check {
	c := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "name": knownvalue.StringExact(name),
		"owner_type": knownvalue.StringExact("organization"), "owner_user_id": knownvalue.Null(),
		"source": knownvalue.StringExact(source), "sync_status": knownvalue.Null(), "last_sync_ended_at": knownvalue.Null(),
		"last_sync_read_sha": knownvalue.Null(), "default_installation_preference": str(defaultPreference),
		"created_at": knownvalue.StringRegexp(regexp.MustCompile(`^2026-10-01T`)),
	}
	if userID != "" {
		c["owner_type"], c["owner_user_id"] = knownvalue.StringExact("user"), knownvalue.StringExact(userID)
	}
	if source != "manual" {
		c["sync_status"] = knownvalue.StringExact("success")
		c["last_sync_ended_at"] = knownvalue.StringExact("2026-09-10T22:15:03Z")
		c["last_sync_read_sha"] = knownvalue.StringExact("9fceb02d0ae598e95dc970b74767f19372d61af8")
	}
	return c
}

// TestPluginMarketplaceDataSources reads one marketplace (and fails on an
// unknown one), then lists three, newest first, one per page, filters them
// and renders no match as [].
func TestPluginMarketplaceDataSources(t *testing.T) {
	t.Parallel()
	srv := newPluginsFake(t, 1)
	srv.putMarketplace("marketplace_eng", "engineering-tools", "", "github", "available")
	srv.putMarketplace("marketplace_sales", "sales", "", "manual", "")
	srv.putMarketplace("marketplace_user_01", "personal", "user_01", "manual", "")
	const one, list = "data.anthropic_plugin_marketplace.test", "data.anthropic_plugin_marketplaces.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:            dataConfig("anthropic_plugin_marketplace", "  id = \"marketplace_eng\"\n"),
				ConfigStateChecks: attrChecks(one, marketplaceCheck("marketplace_eng", "engineering-tools", "", "github", "available")),
			},
			{
				Config:      dataConfig("anthropic_plugin_marketplace", "  id = \"marketplace_gone\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Plugin\s+Marketplace\s+Not\s+Found.*No\s+plugin\s+marketplace\s+marketplace_gone`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetMarketplace, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_plugin_marketplace", "  id = \"marketplace_eng\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+plugin\s+marketplace:\s+API\s+error\s+\(403`),
			},
			{
				Config: dataConfig("anthropic_plugin_marketplaces", ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(list, tfjsonpath.New("marketplaces"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(marketplaceCheck("marketplace_user_01", "personal", "user_01", "manual", nil)),
						knownvalue.ObjectExact(marketplaceCheck("marketplace_sales", "sales", "", "manual", "not_available")),
						knownvalue.ObjectExact(marketplaceCheck("marketplace_eng", "engineering-tools", "", "github", "available")),
					})),
				},
			},
			{
				Config: dataConfig("anthropic_plugin_marketplaces", "  owner_type = \"organization\"\n  source = \"manual\"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(list, tfjsonpath.New("marketplaces"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(marketplaceCheck("marketplace_sales", "sales", "", "manual", "not_available")),
					})),
				},
			},
			{
				Config: dataConfig("anthropic_plugin_marketplaces", "  source = \"gitlab\"\n"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(list, tfjsonpath.New("marketplaces"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			{
				PreConfig:   func() { srv.FailNext(routeListMarketplaces, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_plugin_marketplaces", ""),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+plugin\s+marketplaces:\s+API\s+error\s+\(403`),
			},
		},
	})
	srv.assertWalks(routeListMarketplaces, "",
		walk([]string{pageToken(1), pageToken(2)}, "limit", "1000"),
		walk(nil, "limit", "1000", "owner_type", "organization", "source", "manual"),
		walk(nil, "limit", "1000", "source", "gitlab"))
	srv.assertRequests()
}

// TestPluginDataSources_invalidConfig: IDs of the wrong kind and unknown
// filter values fail before any request.
func TestPluginDataSources_invalidConfig(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ config, err string }{
		"plugin id":           {dataConfig("anthropic_plugin", "  id = \"pluginver_01\"\n"), `must\s+be\s+a\s+plugin\s+ID`},
		"plugins owner":       {dataConfig("anthropic_plugins", "  owner_type = \"team\"\n"), `(?s)owner_type.*value\s+must\s+be\s+one\s+of`},
		"plugins user":        {dataConfig("anthropic_plugins", "  owner_user_id = \"alice\"\n"), `must\s+be\s+a\s+user\s+ID`},
		"plugins marketplace": {dataConfig("anthropic_plugins", "  marketplace_id = \"sales\"\n"), `must\s+be\s+a\s+marketplace\s+ID`},
		"versions plugin":     {dataConfig("anthropic_plugin_versions", "  plugin_id = \"sales\"\n"), `must\s+be\s+a\s+plugin\s+ID`},
		"marketplace id":      {dataConfig("anthropic_plugin_marketplace", "  id = \"sales\"\n"), `must\s+be\s+a\s+marketplace\s+ID`},
		"marketplaces source": {dataConfig("anthropic_plugin_marketplaces", "  source = \"directory\"\n"), `(?s)source.*value\s+must\s+be\s+one\s+of`},
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
}
