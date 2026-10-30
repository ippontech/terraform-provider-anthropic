// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys_test

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

const keyAddr = "anthropic_external_key.test"

const (
	arnParis    = "arn:aws:kms:eu-west-3:111122223333:key/0b1c2d3e-0000-4000-8000-000000000001"
	arnVirginia = "arn:aws:kms:us-east-1:111122223333:key/0b1c2d3e-0000-4000-8000-000000000002"
)

func keyConfig(body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_external_key\" \"test\" {\n" + body + "}\n"
}

// pcCheck is the exact provider_config state: the attributes of fields, the
// others null.
func pcCheck(fields map[string]string) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for _, name := range []string{"type", "kms_arn", "region", "key_name", "vault_uri", "tenant_id", "client_id"} {
		if v, ok := fields[name]; ok {
			checks[name] = knownvalue.StringExact(v)
		} else {
			checks[name] = knownvalue.Null()
		}
	}
	return knownvalue.ObjectExact(checks)
}

func expectKey(n int, displayName string, pc map[string]string, attachment, updatedAt string) []statecheck.StateCheck {
	var name knownvalue.Check = knownvalue.Null()
	if displayName != "" {
		name = knownvalue.StringExact(displayName)
	}
	created := fmt.Sprintf("2026-09-%02dT10:00:00Z", n)
	if updatedAt == "" {
		updatedAt = created
	}
	at := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		at("id", knownvalue.StringExact(keyID(n))),
		at("display_name", name),
		at("geo", knownvalue.StringExact("us")),
		at("provider_config", pcCheck(pc)),
		at("attachment", knownvalue.StringExact(attachment)),
		at("created_at", knownvalue.StringExact(created)),
		at("updated_at", knownvalue.StringExact(updatedAt)),
	}
}

func (f *keysFake) checkGone(ids ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		for _, id := range ids {
			if f.key(id) != nil {
				return fmt.Errorf("external key %s still exists after destroy", id)
			}
		}
		return nil
	}
}

func awsBody(display, arn string) string {
	var name string
	if display != "" {
		name = fmt.Sprintf("  display_name = %q\n", display)
	}
	return name + fmt.Sprintf("  provider_config = {\n    type    = \"aws\"\n    kms_arn = %q\n  }\n", arn)
}

// TestExternalKeyResource_lifecycle registers an AWS key (region derived by
// the API), imports it, renames it, clears the name (sent as null), points
// it at another key while unattached (the region is derived again), and
// deletes it on destroy. Each update carries only what changed.
func TestExternalKeyResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	paris := map[string]string{"type": "aws", "kms_arn": arnParis, "region": "eu-west-3"}
	virginia := map[string]string{"type": "aws", "kms_arn": arnVirginia, "region": "us-east-1"}
	const updated = "2026-09-30T00:00:00Z"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(keyID(1)),
		Steps: []resource.TestStep{
			{
				Config:            keyConfig(awsBody("Prod key", arnParis)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectKey(1, "Prod key", paris, "unattached", ""),
			},
			{
				Config:            keyConfig(awsBody("Prod key", arnParis)),
				ResourceName:      keyAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            keyConfig(awsBody("Production key", arnParis)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "Production key", paris, "unattached", updated),
			},
			{
				Config:            keyConfig(awsBody("", arnParis)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "", paris, "unattached", updated),
			},
			{
				Config:            keyConfig(awsBody("", arnVirginia)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "", virginia, "unattached", updated),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateKey, `{"display_name":"Prod key","provider_config":{"type":"aws","kms_arn":"`+arnParis+`"}}`)
	srv.AssertJSONBodies(routeUpdateKey,
		`{"display_name":"Production key"}`,
		`{"display_name":null}`,
		`{"provider_config":{"type":"aws","kms_arn":"`+arnVirginia+`"}}`)
	srv.AssertCalled(routeDeleteKey, 1)
	srv.assertRequests(t)
}

// TestExternalKeyResource_switchProvider moves an unattached key from Google
// Cloud KMS to Azure Key Vault in place, then drops the optional client_id,
// which is sent as an explicit null.
func TestExternalKeyResource_switchProvider(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	gcp := map[string]string{"type": "gcp", "key_name": "projects/p/locations/us/keyRings/r/cryptoKeys/k"}
	azure := map[string]string{"type": "azure", "key_name": "cmek", "vault_uri": "https://vault-a.vault.azure.net", "tenant_id": "tenant-1", "client_id": "app-1"}
	azureNoClient := map[string]string{"type": "azure", "key_name": "cmek", "vault_uri": "https://vault-a.vault.azure.net", "tenant_id": "tenant-1"}
	const updated = "2026-09-30T00:00:00Z"
	azureBody := func(clientID string) string {
		body := "  geo = \"us\"\n  provider_config = {\n    type      = \"azure\"\n    key_name  = \"cmek\"\n    vault_uri = \"https://vault-a.vault.azure.net\"\n    tenant_id = \"tenant-1\"\n"
		if clientID != "" {
			body += "    client_id = \"" + clientID + "\"\n"
		}
		return body + "  }\n"
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(keyID(1)),
		Steps: []resource.TestStep{
			{
				Config:            keyConfig("  geo = \"us\"\n  provider_config = {\n    type     = \"gcp\"\n    key_name = \"projects/p/locations/us/keyRings/r/cryptoKeys/k\"\n  }\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectKey(1, "", gcp, "unattached", ""),
			},
			{
				Config:            keyConfig(azureBody("app-1")),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "", azure, "unattached", updated),
			},
			{
				Config:            keyConfig(azureBody("")),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "", azureNoClient, "unattached", updated),
			},
		},
	})
	srv.AssertJSONBodies(routeCreateKey, `{"geo":"us","provider_config":{"type":"gcp","key_name":"projects/p/locations/us/keyRings/r/cryptoKeys/k"}}`)
	srv.AssertJSONBodies(routeUpdateKey,
		`{"provider_config":{"type":"azure","key_name":"cmek","vault_uri":"https://vault-a.vault.azure.net","tenant_id":"tenant-1","client_id":"app-1"}}`,
		`{"provider_config":{"type":"azure","key_name":"cmek","vault_uri":"https://vault-a.vault.azure.net","tenant_id":"tenant-1","client_id":null}}`)
}

// TestExternalKeyResource_attached: once a workspace uses the key, the
// refresh reports it attached without a diff; changing the key fails at plan
// time, before any request, while the name still changes; the destroy is
// refused with an explanation, and goes through once no workspace uses it.
func TestExternalKeyResource_attached(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	paris := map[string]string{"type": "aws", "kms_arn": arnParis, "region": "eu-west-3"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(keyID(1)),
		Steps: []resource.TestStep{
			{Config: keyConfig(awsBody("Prod", arnParis)), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.setAttachment(keyID(1), "attached") },
				Config:            keyConfig(awsBody("Prod", arnParis)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: expectKey(1, "Prod", paris, "attached", ""),
			},
			{
				Config:      keyConfig(awsBody("Prod", arnVirginia)),
				ExpectError: regexp.MustCompile(`(?s)Cannot\s+change\s+an\s+attached\s+external\s+key.*provider_config\.kms_arn`),
			},
			{
				Config:            keyConfig(awsBody("Prod (attached)", arnParis)),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, "Prod (attached)", paris, "attached", "2026-09-30T00:00:00Z"),
			},
			{
				Config:      keyConfig(awsBody("Prod (attached)", arnParis)),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)External\s+key\s+is\s+attached\s+to\s+a\s+workspace.*refused\s+to\s+delete`),
			},
			{
				// The last workspace using it is deleted (deleted workspaces
				// do not count): the destroy goes through.
				PreConfig: func() { srv.setAttachment(keyID(1), "unattached") },
				Config:    keyConfig(awsBody("Prod (attached)", arnParis)),
				Destroy:   true,
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateKey, `{"display_name":"Prod (attached)"}`)
	srv.AssertCalled(routeDeleteKey, 2)
}

// TestExternalKeyResource_removedOutsideTerraform: a key deleted behind
// Terraform's back leaves the state on refresh, and the next apply registers
// it again.
func TestExternalKeyResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	config := keyConfig(awsBody("", arnParis))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(keyID(2)),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.drop(keyID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectKey(2, "", map[string]string{"type": "aws", "kms_arn": arnParis, "region": "eu-west-3"}, "unattached", ""),
			},
		},
	})
	srv.AssertCalled(routeCreateKey, 2)
}

// TestExternalKeyResource_deleteNotFound: a key already gone (404) when
// Terraform deletes it is not an error.
func TestExternalKeyResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: keyConfig(awsBody("", arnParis)), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteKey, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteKey, 1)
}

// TestExternalKeyResource_apiErrors: an API error on create, refresh, update
// or delete (of an unattached key) is an error diagnostic naming the
// operation.
func TestExternalKeyResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(keyID(1)),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateKey, http.StatusBadRequest, 1) },
				Config:      keyConfig(awsBody("", arnParis)),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+external\s+key:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{Config: keyConfig(awsBody("", arnParis)), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetKey, http.StatusForbidden, 1) },
				Config:      keyConfig(awsBody("", arnParis)),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+external\s+key:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateKey, http.StatusBadRequest, 1) },
				Config:      keyConfig(awsBody("Renamed", arnParis)),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+external\s+key:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteKey, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+external\s+key:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeCreateKey, 2)
	srv.AssertCalled(routeUpdateKey, 1)
}

// TestExternalKeyResource_invalidProviderConfig: a missing attribute of the
// provider type, or one of another provider, fails the plan before any
// request.
func TestExternalKeyResource_invalidProviderConfig(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ body, err string }{
		"aws without kms_arn": {"  provider_config = { type = \"aws\" }\n", `(?s)Missing\s+required\s+attribute.*"kms_arn"\s+is\s+required\s+when\s+provider_config.type\s+is\s+"aws"`},
		"gcp with kms_arn":    {"  provider_config = {\n    type = \"gcp\"\n    key_name = \"k\"\n    kms_arn = \"" + arnParis + "\"\n  }\n", `(?s)Conflicting\s+attribute.*"kms_arn"\s+must\s+not\s+be\s+set`},
		"unknown provider":    {"  provider_config = { type = \"oci\" }\n", `Invalid\s+Attribute\s+Value\s+Match`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newKeysFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    []resource.TestStep{{Config: keyConfig(tc.body), ExpectError: regexp.MustCompile(tc.err)}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
