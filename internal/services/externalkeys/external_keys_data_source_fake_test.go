// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys_test

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

// The keys of the data source tests: one per provider, the AWS one
// identified by its KMS key ARN as on Claude Platform on AWS (the "/" of its
// key/<uuid> suffix is path-escaped in the item URL).
var (
	awsPC   = map[string]any{"type": "aws", "kms_arn": arnParis, "region": "eu-west-3"}
	gcpPC   = map[string]any{"type": "gcp", "key_name": "projects/p/locations/us/keyRings/r/cryptoKeys/k"}
	azurePC = map[string]any{"type": "azure", "key_name": "cmek", "vault_uri": "https://vault-a.vault.azure.net", "tenant_id": "tenant-1", "client_id": "app-1"}
)

func strFields(pc map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range pc {
		out[k] = v.(string)
	}
	return out
}

func keyObject(id, displayName string, pc map[string]any, attachment string) knownvalue.Check {
	var name knownvalue.Check = knownvalue.Null()
	if displayName != "" {
		name = knownvalue.StringExact(displayName)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "display_name": name, "geo": knownvalue.StringExact("us"),
		"provider_config": pcCheck(strFields(pc)), "attachment": knownvalue.StringExact(attachment),
		"created_at": knownvalue.StringExact("2026-09-01T10:00:00Z"), "updated_at": knownvalue.StringExact("2026-09-01T10:00:00Z"),
	})
}

// TestExternalKeysDataSource_pages lists three keys, two per page, through
// the real Read: it follows next_page with page until next_page is null, and
// asks for the largest page (100).
func TestExternalKeysDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 2)
	srv.put(keyJSON(arnParis, "AWS", awsPC))
	srv.put(keyJSON(keyID(2), "", gcpPC))
	srv.put(keyJSON(keyID(3), "Azure", azurePC))
	srv.setAttachment(keyID(2), "attached")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_external_keys" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_external_keys.test", tfjsonpath.New("external_keys"), knownvalue.ListExact([]knownvalue.Check{
					keyObject(arnParis, "AWS", awsPC, "unattached"),
					keyObject(keyID(2), "", gcpPC, "attached"),
					keyObject(keyID(3), "Azure", azurePC, "unattached"),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListKeys, "limit=100", "limit=100&page="+sdktest.NextPageToken(keyID(2)))
	srv.AssertAllPagesRead(routeListKeys)
	srv.assertRequests(t)
}

// TestExternalKeysDataSource_emptyAndError: no key is an empty list; an API
// error is an error diagnostic.
func TestExternalKeysDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_external_keys" "test" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.anthropic_external_keys.test", tfjsonpath.New("external_keys"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			{
				PreConfig:   func() { srv.FailNext(routeListKeys, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_external_keys" "test" {}`,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+external\s+keys:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
}

// TestExternalKeyDataSource_byID reads one key by its ARN ID (path-escaped);
// an unknown ID is "External Key Not Found"; another error names the read.
func TestExternalKeyDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.put(keyJSON(keyID(1), "Other", gcpPC))
	srv.put(keyJSON(arnParis, "AWS", awsPC))
	const addr = "data.anthropic_external_key.test"
	config := func(id string) string {
		return providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_external_key" "test" { id = "` + id + `" }`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: config(arnParis),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(arnParis)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("display_name"), knownvalue.StringExact("AWS")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("geo"), knownvalue.StringExact("us")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("provider_config"), pcCheck(strFields(awsPC))),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("attachment"), knownvalue.StringExact("unattached")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-09-01T10:00:00Z")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("updated_at"), knownvalue.StringExact("2026-09-01T10:00:00Z")),
				},
			},
			{
				Config:      config("ekey_missing"),
				ExpectError: regexp.MustCompile(`(?s)External\s+Key\s+Not\s+Found.*"ekey_missing"`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetKey, http.StatusForbidden, 1) },
				Config:      config(keyID(1)),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+external\s+key:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	// The ARN read matched the one-segment {external_key_id} route: the "/"
	// of its key/<uuid> suffix went out escaped (an unescaped one adds a
	// segment, which no route matches and the fake fails the test on).
	if got := srv.Requests(routeGetKey); len(got) == 0 || got[0].Path != "/v1/organizations/external_keys/"+arnParis {
		t.Errorf("first read %v, want the ARN key", got)
	}
	srv.assertRequests(t)
}
