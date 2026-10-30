// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/vaults"
)

// seedVault stores a vault as the retrieve reference's example response
// shows it (https://platform.claude.com/docs/en/api/beta/vaults/retrieve),
// created at tick n.
func seedVault(f *vaultsFake, id string, n int, archived bool, metadata map[string]any) {
	o := map[string]any{
		"type": "vault", "id": id, "display_name": "vault " + id, "metadata": metadata,
		"created_at": at(n), "updated_at": at(n + 1), "archived_at": nil,
	}
	if archived {
		o["archived_at"] = at(n + 2)
	}
	f.vaults.Put(o)
}

func vaultObject(id string, n int, archived bool, metadata knownvalue.Check) knownvalue.Check {
	archivedAt := knownvalue.Check(knownvalue.Null())
	if archived {
		archivedAt = knownvalue.StringExact(at(n + 2))
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "display_name": knownvalue.StringExact("vault " + id),
		"metadata": metadata, "type": knownvalue.StringExact("vault"),
		"created_at": knownvalue.StringExact(at(n)), "updated_at": knownvalue.StringExact(at(n + 1)), "archived_at": archivedAt,
	})
}

// seedVaults stores three live vaults and one archived.
func seedVaults(srv *vaultsFake) {
	seedVault(srv, "vlt_a", 1, false, map[string]any{"team": "a"})
	seedVault(srv, "vlt_archived", 11, true, map[string]any{})
	seedVault(srv, "vlt_b", 21, false, map[string]any{})
	seedVault(srv, "vlt_c", 31, false, map[string]any{})
}

// TestVaultDataSource_read reads an archived vault without metadata and a
// live one with: an empty metadata map reads null.
func TestVaultDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	seedVaults(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`
data "anthropic_vault" "archived" {
  id = "vlt_archived"
}

data "anthropic_vault" "live" {
  id = "vlt_a"
}

output "archived" {
  value = data.anthropic_vault.archived
}

output "live" {
  value = data.anthropic_vault.live
}
`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownOutputValue("archived", vaultObject("vlt_archived", 11, true, knownvalue.Null())),
				statecheck.ExpectKnownOutputValue("live", vaultObject("vlt_a", 1, false, stringMap(map[string]string{"team": "a"}))),
			},
		}},
	})
	srv.assertRequestShape()
}

// TestVaultDataSources_errors: an unknown vault is the dedicated not-found
// diagnostic, another API error the generic one, and a list error names the
// list.
func TestVaultDataSources_errors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		block, route string
		fail         int
		pattern      string
	}{
		"vault not found":  {`data "anthropic_vault" "test" { id = "vlt_missing" }`, routeGetVault, 0, `(?s)Vault\s+Not\s+Found.*No\s+vault\s+with\s+ID\s+"vlt_missing"`},
		"vault forbidden":  {`data "anthropic_vault" "test" { id = "vlt_a" }`, routeGetVault, http.StatusForbidden, `(?s)Unable\s+to\s+read\s+vault:.*403\s+Forbidden`},
		"vaults forbidden": {`data "anthropic_vaults" "test" {}`, routeListVaults, http.StatusForbidden, `(?s)Unable\s+to\s+list\s+vaults:.*403\s+Forbidden`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			seedVaults(srv)
			if tc.fail != 0 {
				srv.FailNext(tc.route, tc.fail, 1)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    []resource.TestStep{{Config: config(tc.block), ExpectError: regexp.MustCompile(tc.pattern)}},
			})
			srv.AssertCalled(tc.route, 1)
		})
	}
}

// TestVaultsDataSource_pages lists the vaults two per page through
// next_page, in the API's order; include_archived = true is sent and lists
// the archived one, false (the default) is not sent.
func TestVaultsDataSource_pages(t *testing.T) {
	t.Parallel()
	live := []knownvalue.Check{
		vaultObject("vlt_a", 1, false, stringMap(map[string]string{"team": "a"})),
		vaultObject("vlt_b", 21, false, knownvalue.Null()),
		vaultObject("vlt_c", 31, false, knownvalue.Null()),
	}
	for name, tc := range map[string]struct {
		extra string
		walk  []string
		want  []knownvalue.Check
	}{
		"default": {"", []string{"beta=true", "beta=true&page=" + sdktest.NextPageToken("vlt_b")}, live},
		"include_archived false": {
			"  include_archived = false\n", []string{"beta=true", "beta=true&page=" + sdktest.NextPageToken("vlt_b")}, live,
		},
		"include_archived true": {
			"  include_archived = true\n",
			[]string{"beta=true&include_archived=true", "beta=true&include_archived=true&page=" + sdktest.NextPageToken("vlt_archived")},
			[]knownvalue.Check{live[0], vaultObject("vlt_archived", 11, true, knownvalue.Null()), live[1], live[2]},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 2)
			seedVaults(srv)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config: config("data \"anthropic_vaults\" \"test\" {\n" + tc.extra + "}\n"),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("data.anthropic_vaults.test", tfjsonpath.New("vaults"), knownvalue.ListExact(tc.want)),
					},
				}},
			})
			srv.AssertWalks(routeListVaults, tc.walk...)
			srv.AssertAllPagesRead(routeListVaults)
			srv.assertRequestShape()
		})
	}
}

// TestVaultsDataSource_empty: no vault is an empty list.
func TestVaultsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`data "anthropic_vaults" "test" {}`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_vaults.test", tfjsonpath.New("vaults"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListVaults, "beta=true")
}

// TestVaults_requireAPIKey: both resources and both data sources refuse to
// run with an Admin key alone, before any request.
func TestVaults_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_vault resource":            `resource "anthropic_vault" "test" { display_name = "x" }`,
		"anthropic_vault_credential resource": credentialConfig(bearer("t", 1, ""))[len(config("")):],
		"anthropic_vault":                     `data "anthropic_vault" "test" { id = "vlt_x" }`,
		"anthropic_vaults":                    `data "anthropic_vaults" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestVaults_rejectForeignProviderData covers the Configure guard Terraform
// cannot reach: provider data of another type is an error.
func TestVaults_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]fwresource.Resource{
		"anthropic_vault":            vaults.NewVaultResource(),
		"anthropic_vault_credential": vaults.NewVaultCredentialResource(),
	} {
		var resp fwresource.ConfigureResponse
		r.(fwresource.ResourceWithConfigure).Configure(ctx, fwresource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_vault":  vaults.NewVaultDataSource(),
		"anthropic_vaults": vaults.NewVaultsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
