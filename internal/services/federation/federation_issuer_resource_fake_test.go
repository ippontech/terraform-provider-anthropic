// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

const issuerAddr = "anthropic_federation_issuer.test"

func issuerConfig(body string) string {
	return config(`
resource "anthropic_federation_issuer" "test" {
` + body + `}
`)
}

const githubIssuer = `  name       = "github"
  issuer_url = "https://token.actions.githubusercontent.com"
`

func jwks(typ string, values map[string]knownvalue.Check) knownvalue.Check {
	checks := map[string]knownvalue.Check{
		"type": knownvalue.StringExact(typ), "discovery_base": knownvalue.Null(), "url": knownvalue.Null(),
		"keys": knownvalue.Null(), "ca_cert_pem": knownvalue.Null(),
	}
	maps.Copy(checks, values)
	return knownvalue.ObjectExact(checks)
}

var defaultPollStatus = knownvalue.ObjectExact(map[string]knownvalue.Check{
	"consecutive_failures": knownvalue.Int64Exact(0), "last_fetched_at": knownvalue.Null(),
	"next_poll_at": knownvalue.StringExact("2026-02-01T00:00:00Z"),
})

// checkIssuersArchived is a CheckDestroy: every issuer of the fake is
// archived, there being no delete endpoint.
func (f *federationFake) checkIssuersArchived(*terraform.State) error {
	for _, o := range f.issuers.List(nil) {
		if o["archived_at"] == nil {
			return fmt.Errorf("issuer %s not archived after destroy", o["id"])
		}
	}
	return nil
}

// TestFederationIssuerResource_lifecycle creates an issuer with the defaults
// (OIDC discovery, check_jti, a one-hour lifetime), imports it, then changes
// each attribute in place: only the changed ones are sent; the explicit URL
// and inline key variants of jwks follow, the latter without a poll status.
func TestFederationIssuerResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	keys := `[{"kty":"RSA","kid":"k1","n":"abc","e":"AQAB"}]`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkIssuersArchived,
		Steps: []resource.TestStep{
			{
				Config:           issuerConfig(githubIssuer),
				ConfigPlanChecks: providertest.ExpectAction(issuerAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{
					"id": knownvalue.StringExact(issuerID(1)), "name": knownvalue.StringExact("github"),
					"issuer_url": knownvalue.StringExact("https://token.actions.githubusercontent.com"),
					"jwks":       jwks("discovery", nil), "check_jti": knownvalue.Bool(true),
					"max_jwt_lifetime_seconds": knownvalue.Int64Exact(3600), "jwks_polling_disabled": knownvalue.Null(),
					"jwks_polling_disabled_at": knownvalue.Null(), "poll_status": defaultPollStatus,
					"created_at": knownvalue.StringExact(at(1)), "updated_at": knownvalue.StringExact(at(1)),
					"created_by_actor_id": knownvalue.StringExact("user_001"), "updated_by_actor_id": knownvalue.StringExact("user_001"),
					"archived_at": knownvalue.Null(), "archived_by_actor_id": knownvalue.Null(),
				}),
			},
			{
				Config:            issuerConfig(githubIssuer),
				ResourceName:      issuerAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: issuerConfig(`  name                     = "gitlab"
  issuer_url               = "https://gitlab.example.com"
  check_jti                = false
  max_jwt_lifetime_seconds = 7200
  jwks = {
    type        = "explicit_url"
    url         = "https://gitlab.example.com/oauth/discovery/keys"
    ca_cert_pem = "-----BEGIN CERTIFICATE-----"
  }
`),
				ConfigPlanChecks: providertest.ExpectAction(issuerAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{
					"name": knownvalue.StringExact("gitlab"), "check_jti": knownvalue.Bool(false), "max_jwt_lifetime_seconds": knownvalue.Int64Exact(7200),
					"jwks": jwks("explicit_url", map[string]knownvalue.Check{
						"url": knownvalue.StringExact("https://gitlab.example.com/oauth/discovery/keys"), "ca_cert_pem": knownvalue.StringExact("-----BEGIN CERTIFICATE-----"),
					}),
				}),
			},
			{
				Config: issuerConfig(`  name                     = "gitlab"
  issuer_url               = "https://gitlab.example.com"
  check_jti                = false
  max_jwt_lifetime_seconds = 7200
  jwks = {
    type = "inline"
    keys = jsonencode([{ kty = "RSA", kid = "k1", n = "abc", e = "AQAB" }])
  }
`),
				ConfigPlanChecks: providertest.ExpectAction(issuerAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{
					"jwks":        jwks("inline", map[string]knownvalue.Check{"keys": knownvalue.StringExact(`[{"e":"AQAB","kid":"k1","kty":"RSA","n":"abc"}]`)}),
					"poll_status": knownvalue.Null(),
				}),
			},
			{
				Config: issuerConfig(`  name       = "gitlab"
  issuer_url = "https://gitlab.example.com"
  check_jti  = false
  jwks = {
    type           = "discovery"
    discovery_base = "https://gitlab.example.com/.well-known"
  }
`),
				ConfigPlanChecks: providertest.ExpectAction(issuerAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{
					"max_jwt_lifetime_seconds": knownvalue.Int64Exact(3600),
					"jwks":                     jwks("discovery", map[string]knownvalue.Check{"discovery_base": knownvalue.StringExact("https://gitlab.example.com/.well-known")}),
				}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateIssuer, `{"name": "github", "issuer_url": "https://token.actions.githubusercontent.com",
		"check_jti": true, "max_jwt_lifetime_seconds": 3600, "jwks": {"type": "discovery"}}`)
	srv.AssertJSONBodies(routeUpdateIssuer,
		`{"name": "gitlab", "issuer_url": "https://gitlab.example.com", "check_jti": false, "max_jwt_lifetime_seconds": 7200,
			"jwks": {"type": "explicit_url", "url": "https://gitlab.example.com/oauth/discovery/keys", "ca_cert_pem": "-----BEGIN CERTIFICATE-----"}}`,
		`{"jwks": {"type": "inline", "keys": `+keys+`}}`,
		`{"max_jwt_lifetime_seconds": 3600, "jwks": {"type": "discovery", "discovery_base": "https://gitlab.example.com/.well-known"}}`)
	srv.AssertCalled(routeArchiveIssuer, 1)
	srv.assertRequestShape()
}

// TestFederationIssuerResource_reenablePolling: jwks_polling_disabled =
// false re-enables a poller the API paused (jwks_polling_disabled_at set
// behind Terraform's back); set while polling runs, it sends nothing and
// reads the issuer back instead.
func TestFederationIssuerResource_reenablePolling(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	reenable := issuerConfig(githubIssuer + "  jwks_polling_disabled = false\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: issuerConfig(githubIssuer)},
			{
				// Polling runs: nothing to send, the issuer is read back.
				Config:            reenable,
				ConfigPlanChecks:  providertest.ExpectAction(issuerAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{"jwks_polling_disabled": knownvalue.Bool(false)}),
			},
			{
				PreConfig: func() {
					o, _ := srv.issuers.Get(issuerID(1))
					o["jwks_polling_disabled_at"] = at(30)
					srv.issuers.Put(o)
				},
				Config:            reenable,
				ConfigPlanChecks:  providertest.ExpectAction(issuerAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{"jwks_polling_disabled_at": knownvalue.Null()}),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateIssuer, `{"jwks_polling_disabled": false}`)
}

// TestFederationIssuerResource_reenableRefused: a refused re-enable (400 or
// 403) explains the OAuth restriction on that toggle and keeps the API
// error.
func TestFederationIssuerResource_reenableRefused(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: issuerConfig(githubIssuer)},
			{
				PreConfig: func() {
					o, _ := srv.issuers.Get(issuerID(1))
					o["jwks_polling_disabled_at"] = at(30)
					srv.issuers.Put(o)
					srv.FailNext(routeUpdateIssuer, http.StatusForbidden, 1)
				},
				Config:      issuerConfig(githubIssuer + "  jwks_polling_disabled = false\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+Update\s+Federation\s+Issuer.*403\s+Forbidden.*re-enabled\s+JWKS\s+polling`),
			},
		},
	})
}

// TestFederationIssuerResource_goneOutOfBand: an issuer deleted behind
// Terraform's back is re-created by the next apply; destroy succeeds when the
// archive answers 404.
func TestFederationIssuerResource_goneOutOfBand(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	cfg := issuerConfig(githubIssuer)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:         func() { srv.issuers.Delete(issuerID(1)) },
				Config:            cfg,
				ConfigPlanChecks:  providertest.ExpectAction(issuerAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(issuerAddr, map[string]knownvalue.Check{"id": knownvalue.StringExact(issuerID(2))}),
			},
			{
				PreConfig: func() { srv.FailNext(routeArchiveIssuer, http.StatusNotFound, 1) },
				Config:    cfg,
				Destroy:   true,
			},
		},
	})
	if reqs := srv.Requests(routeArchiveIssuer); len(reqs) != 1 || reqs[0].Fault != "status 404" {
		t.Errorf("archive requests = %+v, want one answered 404", reqs)
	}
}

// TestFederationIssuerResource_errors: an API error on create, refresh,
// update or archive is an error diagnostic, not retried; inline keys that are
// not a JSON array fail before any request.
func TestFederationIssuerResource_errors(t *testing.T) {
	t.Parallel()
	cfg := issuerConfig(githubIssuer)
	for name, tc := range map[string]struct {
		route string
		steps func(fail func()) []resource.TestStep
	}{
		"create": {routeCreateIssuer, func(fail func()) []resource.TestStep {
			return []resource.TestStep{{PreConfig: fail, Config: cfg, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+federation\s+issuer:.*403\s+Forbidden`)}}
		}},
		"refresh": {routeGetIssuer, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+federation\s+issuer:.*403\s+Forbidden`)},
			}
		}},
		"update": {routeUpdateIssuer, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, Config: issuerConfig("  name = \"gh\"\n  issuer_url = \"https://token.actions.githubusercontent.com\"\n"), ExpectError: regexp.MustCompile(`(?s)Client\s+Error.*Unable\s+to\s+update\s+federation\s+issuer:.*403\s+Forbidden`)},
			}
		}},
		"archive": {routeArchiveIssuer, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+federation\s+issuer:.*403\s+Forbidden`)},
			}
		}},
		"invalid keys": {routeCreateIssuer, func(func()) []resource.TestStep {
			return []resource.TestStep{{
				Config:      issuerConfig(githubIssuer + "  jwks = {\n    type = \"inline\"\n    keys = jsonencode({ kty = \"RSA\" })\n  }\n"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+keys.*Failed\s+to\s+parse\s+keys\s+as\s+a\s+JSON\s+array`),
			}}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			steps := tc.steps(func() { srv.FailNext(tc.route, http.StatusForbidden, 1) })
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: factories(srv), Steps: steps})
			faults := 0
			for _, r := range srv.Requests(tc.route) {
				if r.Fault != "" {
					faults++
				}
			}
			want := 1
			if name == "invalid keys" {
				want = 0
			}
			if faults != want {
				t.Errorf("%s: %d failed requests, want %d", tc.route, faults, want)
			}
		})
	}
}
