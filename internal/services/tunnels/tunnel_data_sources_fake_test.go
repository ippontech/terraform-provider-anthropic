// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/tunnels"
)

// fourTunnels stores four tunnels, the first without a display name, and
// archives the second (tick 5).
func (f *tunnelsFake) fourTunnels() {
	f.newTunnel(nil)
	f.newTunnel("second")
	f.newTunnel("third")
	f.newTunnel("fourth")
	f.tunnels.Archive(tunnelID(2))
}

// tunnelObject is the exact data source view of the n-th tunnel of
// fourTunnels.
func tunnelObject(n int) map[string]knownvalue.Check {
	names := map[int]string{2: "second", 3: "third", 4: "fourth"}
	archived := ""
	if n == 2 {
		archived = at(5)
	}
	return map[string]knownvalue.Check{
		"id": knownvalue.StringExact(tunnelID(n)), "display_name": stringOrNull(names[n]),
		"domain": knownvalue.StringExact(domain(tunnelID(n))), "created_at": knownvalue.StringExact(at(n)),
		"archived_at": stringOrNull(archived),
	}
}

func dataChecks(addr string, attrs map[string]knownvalue.Check) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for name, c := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), c))
	}
	return checks
}

func tunnelsList(ns ...int) knownvalue.Check {
	var items []knownvalue.Check
	for _, n := range ns {
		items = append(items, knownvalue.ObjectExact(tunnelObject(n)))
	}
	return knownvalue.ListExact(items)
}

// TestTunnelDataSource_read reads a live tunnel without a display name and
// an archived one, never revealing a token; an unknown ID is an error.
func TestTunnelDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	srv.fourTunnels()
	const addr = "data.anthropic_tunnel.test"
	block := func(id string) string {
		return providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_tunnel" "test" { id = "` + id + `" }`
	}

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{Config: block(tunnelID(1)), ConfigStateChecks: dataChecks(addr, tunnelObject(1))},
			{Config: block(tunnelID(2)), ConfigStateChecks: dataChecks(addr, tunnelObject(2))},
			{Config: block("tnl_missing"), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+tunnel:.*404`)},
		},
	})
	srv.AssertWalks(routeGetTunnel, "beta=true")
	srv.AssertCalled(routeRevealToken, 0)
	srv.assertRequestShape(t)
}

// TestTunnelsDataSource_pages lists the live tunnels over two pages, newest
// first, 100 per page asked.
func TestTunnelsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 2)
	srv.fourTunnels()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config:            providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_tunnels" "test" {}`,
			ConfigStateChecks: dataChecks("data.anthropic_tunnels.test", map[string]knownvalue.Check{"tunnels": tunnelsList(4, 3, 1)}),
		}},
	})
	srv.AssertWalks(routeListTunnels, "beta=true&limit=100", "beta=true&limit=100&page="+sdktest.NextPageToken(tunnelID(3)))
	srv.AssertAllPagesRead(routeListTunnels)
	srv.AssertCalled(routeRevealToken, 0)
	srv.assertRequestShape(t)
}

// TestTunnelsDataSource_includeArchived sends include_archived=true and
// lists the archived tunnel too.
func TestTunnelsDataSource_includeArchived(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 2)
	srv.fourTunnels()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config:            providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_tunnels" "test" { include_archived = true }`,
			ConfigStateChecks: dataChecks("data.anthropic_tunnels.test", map[string]knownvalue.Check{"tunnels": tunnelsList(4, 3, 2, 1)}),
		}},
	})
	srv.AssertWalks(routeListTunnels,
		"beta=true&include_archived=true&limit=100",
		"beta=true&include_archived=true&limit=100&page="+sdktest.NextPageToken(tunnelID(3)))
	srv.AssertAllPagesRead(routeListTunnels)
}

// TestTunnelsDataSource_emptyAndError: no tunnel is an empty list, never
// null; a failed list is an error.
func TestTunnelsDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_tunnels" "test" { include_archived = false }`,
				ConfigStateChecks: dataChecks("data.anthropic_tunnels.test", map[string]knownvalue.Check{
					"tunnels": knownvalue.ListExact([]knownvalue.Check{}),
				}),
			},
			{
				PreConfig:   func() { srv.FailNext(routeListTunnels, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_tunnels" "other" {}`,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+tunnels:.*403`),
			},
		},
	})
	// include_archived = false is sent as such; unset, it is not sent.
	reqs := srv.Requests(routeListTunnels)
	if got := reqs[0].Query.Encode(); got != "beta=true&include_archived=false&limit=100" {
		t.Errorf("first list query %q", got)
	}
	if last := reqs[len(reqs)-1]; last.Query.Encode() != "beta=true&limit=100" || last.Fault != "status 403" {
		t.Errorf("last list query %q (%s), want beta=true&limit=100 (status 403)", last.Query.Encode(), last.Fault)
	}
}

// TestTunnels_requireAuthToken: every resource and data source of the
// package refuses to run with API keys alone, before any request.
func TestTunnels_requireAuthToken(t *testing.T) {
	t.Parallel()
	ca, _ := testAccCA(t, "configure")
	for name, block := range map[string]string{
		"anthropic_tunnel resource":             `resource "anthropic_tunnel" "test" {}`,
		"anthropic_tunnel_certificate resource": fmt.Sprintf("resource \"anthropic_tunnel_certificate\" \"test\" {\n  tunnel_id = \"tnl_x\"\n  ca_certificate_pem = %q\n}", ca),
		"anthropic_tunnel":                      `data "anthropic_tunnel" "test" { id = "tnl_x" }`,
		"anthropic_tunnels":                     `data "anthropic_tunnels" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newTunnelsFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey, sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+OAuth\s+Token`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestTunnels_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestTunnels_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_tunnel":             tunnels.NewTunnelResource(),
		"anthropic_tunnel_certificate": tunnels.NewTunnelCertificateResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_tunnel":  tunnels.NewTunnelDataSource(),
		"anthropic_tunnels": tunnels.NewTunnelsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
