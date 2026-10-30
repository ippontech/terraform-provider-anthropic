// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

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

const tunnelAddr = "anthropic_tunnel.test"

// tunnelID and certID are the IDs the fake gives to the n-th object of each
// collection.
func tunnelID(n int) string { return fmt.Sprintf("tnl_%024d", n) }
func certID(n int) string   { return fmt.Sprintf("tcrt_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func tunnelBlock(extra string) string {
	return `
resource "anthropic_tunnel" "test" {
` + extra + `}
`
}

func tunnelConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.AuthToken) + tunnelBlock(extra)
}

func stringOrNull(s string) knownvalue.Check {
	if s == "" {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(s)
}

// expectTunnel checks every attribute of the n-th tunnel, created at the
// createdAt tick, holding the token-th token.
func expectTunnel(n int, displayName, rotation string, createdAt, token int) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(tunnelAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(tunnelID(n))),
		check("display_name", stringOrNull(displayName)),
		check("domain", knownvalue.StringExact(domain(tunnelID(n)))),
		check("created_at", knownvalue.StringExact(at(createdAt))),
		check("token_rotation", stringOrNull(rotation)),
		check("token", knownvalue.StringExact(tokenValue(token))),
		check("token_id", knownvalue.StringExact(tokenID(token))),
	}
}

// checkAllArchived is a CheckDestroy: there is no delete endpoint, so every
// tunnel and certificate left in the fake must be archived.
func (f *tunnelsFake) checkAllArchived(*terraform.State) error {
	for _, c := range []*struct {
		name string
		objs []map[string]any
	}{{"tunnel", f.tunnels.List(nil)}, {"certificate", f.certs.List(nil)}} {
		for _, o := range c.objs {
			if o["archived_at"] == nil {
				return fmt.Errorf("%s %s is still live after destroy", c.name, o["id"])
			}
		}
	}
	return nil
}

// TestTunnelResource_lifecycle creates a tunnel and reveals its token,
// imports it (one more reveal), records a first token_rotation without
// rotating, rotates on a change, replaces the tunnel on a display_name
// change and archives both.
func TestTunnelResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				Config:            tunnelConfig("  display_name = \"corp\"\n"),
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectTunnel(1, "corp", "", 1, 1),
			},
			{Config: tunnelConfig("  display_name = \"corp\"\n"), ResourceName: tunnelAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:            tunnelConfig("  display_name = \"corp\"\n  token_rotation = \"v1\"\n"),
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectTunnel(1, "corp", "v1", 1, 1),
			},
			{
				Config:            tunnelConfig("  display_name = \"corp\"\n  token_rotation = \"v2\"\n"),
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectTunnel(1, "corp", "v2", 1, 2),
			},
			{
				Config:            tunnelConfig("  display_name = \"corp-2\"\n  token_rotation = \"v2\"\n"),
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectTunnel(2, "corp-2", "v2", 3, 3),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateTunnel, `{"display_name": "corp"}`, `{"display_name": "corp-2"}`)
	srv.AssertJSONBodies(routeRotateToken, `{"reason": "token_rotation changed in the Terraform configuration"}`)
	// Create, import, the replacement's create: never on a refresh.
	want := []string{
		"/v1/tunnels/" + tunnelID(1) + "/reveal_token ",
		"/v1/tunnels/" + tunnelID(1) + "/reveal_token ",
		"/v1/tunnels/" + tunnelID(2) + "/reveal_token ",
	}
	if got := srv.requestPaths(routeRevealToken); !slices.Equal(got, want) {
		t.Errorf("reveals %q, want %q", got, want)
	}
	srv.AssertCalled(routeArchiveTunnel, 2)
	srv.assertRequestShape(t)
}

// TestTunnelResource_goneOutsideTerraform: a tunnel archived behind
// Terraform's back, then one deleted (404), leave the state and are created
// again; a 404 on the archive of the destroy is success.
func TestTunnelResource_goneOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	config := tunnelConfig("")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.tunnels.Archive(tunnelID(1)) },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectTunnel(2, "", "", 3, 2),
			},
			{
				PreConfig:         func() { srv.tunnels.Delete(tunnelID(2)) },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectTunnel(3, "", "", 4, 3),
			},
			{
				PreConfig:        func() { srv.FailNext(routeArchiveTunnel, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AuthToken),
				ConfigPlanChecks: providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	// No display_name: the create body is empty.
	srv.AssertJSONBodies(routeCreateTunnel, `{}`, `{}`, `{}`)
	srv.AssertCalled(routeArchiveTunnel, 1)
	srv.assertRequestShape(t)
}

// TestTunnelResource_revealFailure: a tunnel whose token cannot be revealed
// right after its creation is kept in state, tainted, and the next apply
// replaces it.
func TestTunnelResource_revealFailure(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	config := tunnelConfig("  display_name = \"tainted\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeRevealToken, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Tunnel\s+` + tunnelID(1) + `\s+was\s+created,\s+but\s+its\s+token\s+could\s+not\s+be\s+revealed`),
			},
			{
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectTunnel(2, "tainted", "", 3, 2),
			},
		},
	})

	if got, want := srv.requestPaths(routeArchiveTunnel), []string{
		"/v1/tunnels/" + tunnelID(1) + "/archive ",
		"/v1/tunnels/" + tunnelID(2) + "/archive ",
	}; !slices.Equal(got, want) {
		t.Errorf("archives %q, want %q", got, want)
	}
}

// TestTunnelResource_apiErrors: a 500 on create is not replayed (creation
// is not idempotent), and an API error on refresh, rotation or archive
// reaches the operator with the provider's summary.
func TestTunnelResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateTunnel, http.StatusInternalServerError, 1) },
				Config:      tunnelConfig("  token_rotation = \"a\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+tunnel:.*500`),
			},
			{Config: tunnelConfig("  token_rotation = \"a\"\n"), ConfigPlanChecks: providertest.ExpectAction(tunnelAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetTunnel, http.StatusForbidden, 1) },
				Config:      tunnelConfig("  token_rotation = \"a\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+tunnel:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeRotateToken, http.StatusBadRequest, 1) },
				Config:      tunnelConfig("  token_rotation = \"b\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+rotate\s+tunnel\s+token:.*400`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeArchiveTunnel, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.AuthToken),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+tunnel:.*400`),
			},
		},
	})

	srv.AssertCalled(routeCreateTunnel, 2)
	srv.AssertCalled(routeRotateToken, 1)
	srv.AssertCalled(routeArchiveTunnel, 2)
	srv.assertRequestShape(t)
}
