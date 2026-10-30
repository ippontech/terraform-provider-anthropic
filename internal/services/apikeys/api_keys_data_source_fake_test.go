// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
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

const keysAddr = "data.anthropic_api_keys.test"

func keyItem(n, shape int, name, status string) knownvalue.Check {
	return knownvalue.ObjectExact(keyChecks(n, shape, name, status))
}

// TestAPIKeysDataSource_pages lists five keys of every shape, served two per
// page, through the real Read: it follows last_id with after_id until
// has_more is false, keeps the API's order and asks for the largest page.
func TestAPIKeysDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 2)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "One", "active"))
	srv.keys.Put(keyJSON(2, orgServiceAccountKey, "Two", "inactive"))
	srv.keys.Put(keyJSON(3, legacyDefaultKey, "Three", "archived"))
	srv.keys.Put(keyJSON(4, workspaceUserKey, "Four", "expired"))
	srv.keys.Put(keyJSON(5, orgServiceAccountKey, "Five", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_api_keys" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("api_keys"), knownvalue.ListExact([]knownvalue.Check{
					keyItem(1, workspaceUserKey, "One", "active"),
					keyItem(2, orgServiceAccountKey, "Two", "inactive"),
					keyItem(3, legacyDefaultKey, "Three", "archived"),
					keyItem(4, workspaceUserKey, "Four", "expired"),
					keyItem(5, orgServiceAccountKey, "Five", "active"),
				})),
			},
		}},
	})

	srv.AssertWalks(routeListKeys, "limit=1000", "after_id="+keyID(2)+"&limit=1000", "after_id="+keyID(4)+"&limit=1000")
	srv.AssertAllPagesRead(routeListKeys)
	srv.assertAdminRequests(t)
}

// TestAPIKeysDataSource_filters sends the three filters on every page and
// keeps them in state.
func TestAPIKeysDataSource_filters(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 1)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "One", "active"))
	srv.keys.Put(keyJSON(2, workspaceUserKey, "Two", "inactive"))
	srv.keys.Put(keyJSON(3, orgServiceAccountKey, "Three", "active"))
	srv.keys.Put(keyJSON(4, workspaceUserKey, "Four", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_api_keys" "test" {
  status             = "active"
  workspace_id       = "wrkspc_team"
  created_by_user_id = "user_alice"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("api_keys"), knownvalue.ListExact([]knownvalue.Check{
					keyItem(1, workspaceUserKey, "One", "active"),
					keyItem(4, workspaceUserKey, "Four", "active"),
				})),
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("status"), knownvalue.StringExact("active")),
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact("wrkspc_team")),
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("created_by_user_id"), knownvalue.StringExact("user_alice")),
			},
		}},
	})
	const filters = "created_by_user_id=user_alice&limit=1000&status=active&workspace_id=wrkspc_team"
	srv.AssertWalks(routeListKeys, filters, "after_id="+keyID(1)+"&"+filters)
	srv.AssertAllPagesRead(routeListKeys)
}

// TestAPIKeysDataSource_empty: no key is an empty list.
func TestAPIKeysDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_api_keys" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(keysAddr, tfjsonpath.New("api_keys"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListKeys, "limit=1000")
}

// TestAPIKeysDataSource_error: an API error is an error diagnostic.
func TestAPIKeysDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.FailNext(routeListKeys, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_api_keys" "test" {}`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+API\s+keys:\s+API\s+error\s+\(403\s+permission_error\)`),
		}},
	})
	srv.AssertCalled(routeListKeys, 1)
}

// TestAPIKeyDataSource_byID maps one key by ID, nullable fields included.
func TestAPIKeyDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "One", "active"))
	srv.keys.Put(keyJSON(2, orgServiceAccountKey, "Two", "inactive"))

	var checks []statecheck.StateCheck
	for attr, c := range keyChecks(2, orgServiceAccountKey, "Two", "inactive") {
		checks = append(checks, statecheck.ExpectKnownValue("data.anthropic_api_key.test", tfjsonpath.New(attr), c))
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:            providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_api_key" "test" { id = "` + keyID(2) + `" }`,
			ConfigStateChecks: checks,
		}},
	})
	for _, r := range srv.Requests(routeGetKey) {
		if r.Path != "/v1/organizations/api_keys/"+keyID(2) {
			t.Errorf("read %s, want the configured key", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestAPIKeyDataSource_unknownID: an unknown key is an error.
func TestAPIKeyDataSource_unknownID(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_api_key" "test" { id = "apikey_missing" }`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+API\s+key:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeGetKey, 1)
}
