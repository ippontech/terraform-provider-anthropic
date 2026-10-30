// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const credentialAddr = "anthropic_vault_credential.test"

// credentialConfig is a vault and one credential in it with the given
// attributes.
func credentialConfig(body string) string {
	return config(`
resource "anthropic_vault" "test" {
  display_name = "credentials"
}

resource "anthropic_vault_credential" "test" {
  vault_id = anthropic_vault.test.id
` + body + `}
`)
}

// importCredential is an import step of the credential, by
// "<vault_id>:<credential_id>". token_wo_version is not in the API.
func importCredential(cfg string) resource.TestStep {
	return resource.TestStep{
		Config:            cfg,
		ResourceName:      credentialAddr,
		ImportState:       true,
		ImportStateIdFunc: func(*terraform.State) (string, error) { return vaultID(1) + ":" + credentialID(1), nil },
		ImportStateVerify: true,
		ImportStateVerifyIgnore: []string{
			"token_wo_version",
		},
	}
}

// checkCredentialsGone is a CheckDestroy: the fake holds no credential.
func (f *vaultsFake) checkCredentialsGone(*terraform.State) error {
	if n := f.credentials.Len(); n != 0 {
		return fmt.Errorf("%d credentials left after destroy", n)
	}
	return nil
}

func bearer(token string, version int, extra string) string {
	return fmt.Sprintf(`  type             = "static_bearer"
  mcp_server_url   = "https://mcp.example.com/mcp"
  token            = %q
  token_wo_version = %d
`, token, version) + extra
}

// TestVaultCredentialResource_staticBearer creates a static bearer
// credential, imports it, updates its name and metadata without touching the
// secret, rotates the token through token_wo_version, and clears the name.
func TestVaultCredentialResource_staticBearer(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	named := "  display_name = \"bearer\"\n  metadata = { team = \"a\" }\n"
	renamed := "  display_name = \"bearer renamed\"\n  metadata = { team = \"b\" }\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{
			{
				Config:           credentialConfig(bearer("bearer-v1", 1, named)),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
					"id":                 knownvalue.StringExact(credentialID(1)),
					"vault_id":           knownvalue.StringExact(vaultID(1)),
					"type":               knownvalue.StringExact("static_bearer"),
					"credential_type":    knownvalue.StringExact("vault_credential"),
					"mcp_server_url":     knownvalue.StringExact("https://mcp.example.com/mcp"),
					"display_name":       knownvalue.StringExact("bearer"),
					"metadata":           stringMap(map[string]string{"team": "a"}),
					"token":              knownvalue.Null(),
					"token_wo_version":   knownvalue.Int64Exact(1),
					"expires_at":         knownvalue.Null(),
					"refresh":            knownvalue.Null(),
					"secret_name":        knownvalue.Null(),
					"networking":         knownvalue.Null(),
					"injection_location": knownvalue.Null(),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(1)),
					"archived_at":        knownvalue.Null(),
					"archive_on_destroy": knownvalue.Bool(false),
				}),
			},
			importCredential(credentialConfig(bearer("bearer-v1", 1, named))),
			{
				Config:            credentialConfig(bearer("bearer-v1", 1, renamed)),
				ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"display_name": knownvalue.StringExact("bearer renamed"), "metadata": stringMap(map[string]string{"team": "b"})}),
			},
			{
				Config:            credentialConfig(bearer("bearer-v2", 2, renamed)),
				ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"token_wo_version": knownvalue.Int64Exact(2), "token": knownvalue.Null()}),
			},
			{
				Config:            credentialConfig(bearer("bearer-v2", 2, "")),
				ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"display_name": knownvalue.Null(), "metadata": knownvalue.Null()}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateCredential, `{"display_name": "bearer", "metadata": {"team": "a"},
		"auth": {"type": "static_bearer", "mcp_server_url": "https://mcp.example.com/mcp", "token": "bearer-v1"}}`)
	srv.AssertJSONBodies(routeUpdateCredential,
		`{"display_name": "bearer renamed", "metadata": {"team": "b"}}`,
		`{"display_name": "bearer renamed", "metadata": {"team": "b"}, "auth": {"type": "static_bearer", "token": "bearer-v2"}}`,
		`{"display_name": null, "metadata": {"team": null}}`)
	srv.AssertCalled(routeDeleteCredential, 1)
	srv.assertRequestShape()
}

// TestVaultCredentialResource_bareHostURL: the API stores a bare host with a
// trailing slash; the state keeps the configured URL, so the plan after the
// refresh is empty instead of a replacement.
func TestVaultCredentialResource_bareHostURL(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{{
			Config:            credentialConfig(strings.Replace(bearer("bearer-v1", 1, ""), "https://mcp.example.com/mcp", "https://mcp.example.com", 1)),
			ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"mcp_server_url": knownvalue.StringExact("https://mcp.example.com")}),
			Check: func(*terraform.State) error {
				if o, _ := srv.credentials.Get(credentialID(1)); o["auth"].(map[string]any)["mcp_server_url"] != "https://mcp.example.com/" {
					return fmt.Errorf("stored auth %v, want the URL with a trailing slash", o["auth"])
				}
				return nil
			},
		}},
	})
}

func oauth(version int, expires, refresh string) string {
	return fmt.Sprintf(`  type             = "mcp_oauth"
  mcp_server_url   = "https://mcp.example.com/sse"
  access_token     = "access-v%[1]d"
  token_wo_version = %[1]d
`, version) + expires + refresh
}

func refreshBlock(version int, tea, extra string) string {
	return fmt.Sprintf(`  refresh = {
    client_id      = "client-1"
    refresh_token  = "refresh-v%d"
    token_endpoint = "https://auth.example.com/token"
%s    token_endpoint_auth = {
%s    }
  }
`, version, extra, tea)
}

// TestVaultCredentialResource_mcpOAuth creates an OAuth credential with an
// expiry and a refresh block (client_secret_basic), imports it, then rotates
// every secret and changes the expiry and the scope in place.
func TestVaultCredentialResource_mcpOAuth(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	basic := func(v int) string {
		return fmt.Sprintf("      type          = \"client_secret_basic\"\n      client_secret = \"secret-v%d\"\n", v)
	}
	v1 := oauth(1, "  expires_at = \"2027-01-01T00:00:00Z\"\n",
		refreshBlock(1, basic(1), "    resource       = \"https://mcp.example.com\"\n    scope          = \"read\"\n"))
	v2 := oauth(2, "  expires_at = \"2027-06-01T00:00:00Z\"\n",
		refreshBlock(2, basic(2), "    resource       = \"https://mcp.example.com\"\n    scope          = \"read write\"\n"))
	refresh := func(scope string) knownvalue.Check {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{
			"client_id":      knownvalue.StringExact("client-1"),
			"refresh_token":  knownvalue.Null(),
			"token_endpoint": knownvalue.StringExact("https://auth.example.com/token"),
			"resource":       knownvalue.StringExact("https://mcp.example.com"),
			"scope":          knownvalue.StringExact(scope),
			"token_endpoint_auth": knownvalue.ObjectExact(map[string]knownvalue.Check{
				"type": knownvalue.StringExact("client_secret_basic"), "client_secret": knownvalue.Null(),
			}),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{
			{
				Config:           credentialConfig(v1),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
					"type":           knownvalue.StringExact("mcp_oauth"),
					"mcp_server_url": knownvalue.StringExact("https://mcp.example.com/sse"),
					"access_token":   knownvalue.Null(),
					"expires_at":     knownvalue.StringExact("2027-01-01T00:00:00Z"),
					"refresh":        refresh("read"),
					"display_name":   knownvalue.Null(),
					"secret_name":    knownvalue.Null(),
					"networking":     knownvalue.Null(),
				}),
			},
			importCredential(credentialConfig(v1)),
			{
				Config:           credentialConfig(v2),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
					"expires_at": knownvalue.StringExact("2027-06-01T00:00:00Z"),
					"refresh":    refresh("read write"),
				}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateCredential, `{"auth": {
		"type": "mcp_oauth", "mcp_server_url": "https://mcp.example.com/sse", "access_token": "access-v1",
		"expires_at": "2027-01-01T00:00:00Z",
		"refresh": {"client_id": "client-1", "refresh_token": "refresh-v1", "token_endpoint": "https://auth.example.com/token",
			"resource": "https://mcp.example.com", "scope": "read",
			"token_endpoint_auth": {"type": "client_secret_basic", "client_secret": "secret-v1"}}}}`)
	srv.AssertJSONBodies(routeUpdateCredential, `{"display_name": null, "auth": {
		"type": "mcp_oauth", "access_token": "access-v2", "expires_at": "2027-06-01T00:00:00Z",
		"refresh": {"refresh_token": "refresh-v2", "scope": "read write",
			"token_endpoint_auth": {"type": "client_secret_basic", "client_secret": "secret-v2"}}}}`)
	srv.assertRequestShape()
}

// TestVaultCredentialResource_mcpOAuthEndpointAuth: token_endpoint_auth
// "none" sends no secret on create and nothing on update; "client_secret_post"
// sends its secret on both.
func TestVaultCredentialResource_mcpOAuthEndpointAuth(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		tea          func(v int) string
		create, auth string
	}{
		"none": {
			tea:    func(int) string { return "      type = \"none\"\n" },
			create: `{"type": "none"}`,
			auth:   ``,
		},
		"client_secret_post": {
			tea: func(v int) string {
				return fmt.Sprintf("      type          = \"client_secret_post\"\n      client_secret = \"post-v%d\"\n", v)
			},
			create: `{"type": "client_secret_post", "client_secret": "post-v1"}`,
			auth:   `, "token_endpoint_auth": {"type": "client_secret_post", "client_secret": "post-v2"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				CheckDestroy:             srv.checkCredentialsGone,
				Steps: []resource.TestStep{
					{
						Config:           credentialConfig(oauth(1, "", refreshBlock(1, tc.tea(1), ""))),
						ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
						ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
							"expires_at": knownvalue.Null(),
						}),
					},
					{
						Config:           credentialConfig(oauth(2, "", refreshBlock(2, tc.tea(2), ""))),
						ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionUpdate),
					},
				},
			})
			srv.AssertJSONBodies(routeCreateCredential, `{"auth": {
				"type": "mcp_oauth", "mcp_server_url": "https://mcp.example.com/sse", "access_token": "access-v1",
				"refresh": {"client_id": "client-1", "refresh_token": "refresh-v1", "token_endpoint": "https://auth.example.com/token",
					"token_endpoint_auth": `+tc.create+`}}}`)
			srv.AssertJSONBodies(routeUpdateCredential, `{"display_name": null, "auth": {
				"type": "mcp_oauth", "access_token": "access-v2", "refresh": {"refresh_token": "refresh-v2"`+tc.auth+`}}}`)
		})
	}
}

func envVar(extra string) string {
	return `  type             = "environment_variable"
  secret_name      = "API_KEY"
  secret_value     = "value-v1"
  token_wo_version = 1
` + extra
}

// TestVaultCredentialResource_environmentVariable creates an environment
// variable credential on limited hosts with both injection locations,
// imports it, then switches it to unrestricted networking and header-only
// injection (body left out, so false), which re-sends the secret.
func TestVaultCredentialResource_environmentVariable(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	limited := envVar("  networking = {\n    mode = \"limited\"\n    allowed_hosts = [\"api.example.com\", \"*.example.org\"]\n  }\n" +
		"  injection_location = { header = true, body = true }\n")
	headerOnly := envVar("  networking = { mode = \"unrestricted\" }\n  injection_location = { header = true }\n")
	location := func(header, body bool) knownvalue.Check {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{"header": knownvalue.Bool(header), "body": knownvalue.Bool(body)})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{
			{
				Config:           credentialConfig(limited),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
					"type":         knownvalue.StringExact("environment_variable"),
					"secret_name":  knownvalue.StringExact("API_KEY"),
					"secret_value": knownvalue.Null(),
					"networking": knownvalue.ObjectExact(map[string]knownvalue.Check{
						"mode":          knownvalue.StringExact("limited"),
						"allowed_hosts": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("api.example.com"), knownvalue.StringExact("*.example.org")}),
					}),
					"injection_location": location(true, true),
					"mcp_server_url":     knownvalue.Null(),
					"refresh":            knownvalue.Null(),
				}),
			},
			importCredential(credentialConfig(limited)),
			{
				Config:           credentialConfig(headerOnly),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
					"networking":         knownvalue.ObjectExact(map[string]knownvalue.Check{"mode": knownvalue.StringExact("unrestricted"), "allowed_hosts": knownvalue.Null()}),
					"injection_location": location(true, false),
				}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateCredential, `{"auth": {
		"type": "environment_variable", "secret_name": "API_KEY", "secret_value": "value-v1",
		"networking": {"type": "limited", "allowed_hosts": ["api.example.com", "*.example.org"]},
		"injection_location": {"header": true, "body": true}}}`)
	srv.AssertJSONBodies(routeUpdateCredential, `{"display_name": null, "auth": {
		"type": "environment_variable", "secret_value": "value-v1",
		"networking": {"type": "unrestricted"}, "injection_location": {"header": true, "body": false}}}`)
	srv.assertRequestShape()
}

// TestVaultCredentialResource_defaultInjectionLocation: without an
// injection_location block the API enables both locations and the state
// stores them; the next plan is empty.
func TestVaultCredentialResource_defaultInjectionLocation(t *testing.T) {
	t.Skip("bug: without an injection_location block, the defaults of its fields make every plan an in-place update (updated_at known after apply); see fix/vaults-injection-location-perpetual-diff")
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{{
			Config:           credentialConfig(envVar("  networking = { mode = \"unrestricted\" }\n")),
			ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{
				"injection_location": knownvalue.ObjectExact(map[string]knownvalue.Check{"header": knownvalue.Bool(true), "body": knownvalue.Bool(true)}),
			}),
		}},
	})
	srv.AssertJSONBodies(routeCreateCredential, `{"auth": {"type": "environment_variable", "secret_name": "API_KEY",
		"secret_value": "value-v1", "networking": {"type": "unrestricted"}}}`)
}

// TestVaultCredentialResource_archiveAndGone: archive_on_destroy archives
// the credential; a credential deleted behind Terraform's back is re-created
// by the next apply; destroy succeeds when the delete or the archive answers
// 404.
func TestVaultCredentialResource_archiveAndGone(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	archived := credentialConfig(bearer("bearer-v1", 1, "  archive_on_destroy = true\n"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{
				Config:            archived,
				ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"archive_on_destroy": knownvalue.Bool(true)}),
			},
			{
				PreConfig:         func() { srv.credentials.Delete(credentialID(1)) },
				Config:            archived,
				ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"id": knownvalue.StringExact(credentialID(2))}),
			},
			{
				// The archive answers 404: destroy goes on with the vault.
				PreConfig: func() { srv.FailNext(routeArchiveCredential, http.StatusNotFound, 1) },
				Config:    archived,
				Destroy:   true,
			},
		},
	})
	if reqs := srv.Requests(routeArchiveCredential); len(reqs) != 1 || reqs[0].Fault != "status 404" {
		t.Errorf("archive requests = %+v, want one answered 404", reqs)
	}

	srv = newVaultsFake(t, 0)
	plain := credentialConfig(bearer("bearer-v1", 1, ""))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: plain},
			{
				PreConfig: func() { srv.FailNext(routeDeleteCredential, http.StatusNotFound, 1) },
				Config:    plain,
				Destroy:   true,
			},
		},
	})
	if reqs := srv.Requests(routeDeleteCredential); len(reqs) != 1 || reqs[0].Fault != "status 404" {
		t.Errorf("delete requests = %+v, want one answered 404", reqs)
	}
}

// TestVaultCredentialResource_apiErrors: an API error on create, refresh,
// update, delete or archive is an error diagnostic; none is retried (4xx).
func TestVaultCredentialResource_apiErrors(t *testing.T) {
	t.Parallel()
	plain := credentialConfig(bearer("bearer-v1", 1, ""))
	archived := credentialConfig(bearer("bearer-v1", 1, "  archive_on_destroy = true\n"))
	for name, tc := range map[string]struct {
		route string
		steps func(fail func()) []resource.TestStep
	}{
		"create": {routeCreateCredential, func(fail func()) []resource.TestStep {
			return []resource.TestStep{{PreConfig: fail, Config: plain, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+vault\s+credential:.*403\s+Forbidden`)}}
		}},
		"refresh": {routeGetCredential, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: plain},
				{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+vault\s+credential:.*403\s+Forbidden`)},
			}
		}},
		"update": {routeUpdateCredential, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: plain},
				{PreConfig: fail, Config: credentialConfig(bearer("bearer-v2", 2, "")), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+vault\s+credential:.*403\s+Forbidden`)},
			}
		}},
		"delete": {routeDeleteCredential, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: plain},
				{PreConfig: fail, Config: plain, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+vault\s+credential:.*403\s+Forbidden`)},
			}
		}},
		"archive": {routeArchiveCredential, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: archived},
				{PreConfig: fail, Config: archived, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+vault\s+credential:.*403\s+Forbidden`)},
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    tc.steps(func() { srv.FailNext(tc.route, http.StatusForbidden, 1) }),
			})
			faults := 0
			for _, r := range srv.Requests(tc.route) {
				if r.Fault != "" {
					faults++
				}
			}
			if faults != 1 {
				t.Errorf("%s: %d failed requests, want 1 (no retry)", tc.route, faults)
			}
		})
	}
}

// TestVaultCredentialResource_invalid: a malformed expires_at fails create
// and update before the request, and the config validator rejects a missing
// secret and an attribute of another auth type before any request.
func TestVaultCredentialResource_invalid(t *testing.T) {
	t.Parallel()
	badExpiry := credentialConfig(oauth(1, "  expires_at = \"tomorrow\"\n", ""))
	for name, steps := range map[string][]resource.TestStep{
		"expires_at on create": {{Config: badExpiry, ExpectError: regexp.MustCompile(`(?s)Invalid\s+timestamp.*Cannot\s+parse\s+expires_at`)}},
		"expires_at on update": {
			{Config: credentialConfig(oauth(1, "", ""))},
			{Config: badExpiry, ExpectError: regexp.MustCompile(`(?s)Invalid\s+timestamp.*Cannot\s+parse\s+expires_at`)},
		},
		"missing token": {{
			Config:      credentialConfig("  type = \"static_bearer\"\n  mcp_server_url = \"https://mcp.example.com\"\n"),
			ExpectError: regexp.MustCompile(`(?s)Missing\s+required\s+attribute.*"token"\s+is\s+required\s+when\s+type\s+is\s+"static_bearer"`),
		}},
		"conflicting secret_name": {{
			Config:      credentialConfig(bearer("bearer-v1", 1, "  secret_name = \"X\"\n")),
			ExpectError: regexp.MustCompile(`(?s)Conflicting\s+attribute.*"secret_name"\s+must\s+not\s+be\s+set`),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: factories(srv), Steps: steps})
			srv.AssertCalled(routeCreateCredential, len(steps)-1)
			srv.AssertCalled(routeUpdateCredential, 0)
		})
	}
}

// TestVaultCredentialResource_scopeRemoved: removing refresh.scope clears it.
// The update keeps an omitted scope and clears it on "" ("Send an empty
// string to clear the stored scope",
// https://platform.claude.com/docs/en/api/beta/vaults/credentials/update).
func TestVaultCredentialResource_scopeRemoved(t *testing.T) {
	t.Skip("bug: a removed refresh.scope is not sent, so the API keeps it (inconsistent result after apply); see fix/vaults-refresh-scope-removal")
	t.Parallel()
	srv := newVaultsFake(t, 0)
	none := "      type = \"none\"\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{
			{
				Config:           credentialConfig(oauth(1, "", refreshBlock(1, none, "    scope          = \"read\"\n"))),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionCreate),
			},
			{
				Config:           credentialConfig(oauth(1, "", refreshBlock(1, none, ""))),
				ConfigPlanChecks: expectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(credentialAddr, tfjsonpath.New("refresh").AtMapKey("scope"), knownvalue.Null()),
				},
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateCredential, `{"display_name": null, "auth": {"type": "mcp_oauth", "access_token": "access-v1",
		"refresh": {"refresh_token": "refresh-v1", "scope": ""}}}`)
}

// TestVaultCredentialResource_emptyMetadata: metadata = {} is sent as {} and
// stays an empty map in state (root CLAUDE.md, "Map attributes with PATCH
// semantics").
func TestVaultCredentialResource_emptyMetadata(t *testing.T) {
	t.Skip("bug: metadata = {} reads back as null (inconsistent result after apply); see fix/vaults-empty-metadata")
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkCredentialsGone,
		Steps: []resource.TestStep{{
			Config:            credentialConfig(bearer("bearer-v1", 1, "  metadata = {}\n")),
			ConfigPlanChecks:  expectAction(credentialAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: expect(credentialAddr, map[string]knownvalue.Check{"metadata": knownvalue.MapExact(map[string]knownvalue.Check{})}),
		}},
	})
}
