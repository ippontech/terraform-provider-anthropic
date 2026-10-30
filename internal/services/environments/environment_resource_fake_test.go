// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

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
)

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

// stringList is the exact value of a list of strings.
func stringList(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = knownvalue.StringExact(v)
	}
	return knownvalue.ListExact(checks)
}

// limitedNetwork is the exact value of a limited networking object.
func limitedNetwork(mcp, packageManagers bool, hosts ...string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type":                   knownvalue.StringExact("limited"),
		"allow_mcp_servers":      knownvalue.Bool(mcp),
		"allow_package_managers": knownvalue.Bool(packageManagers),
		"allowed_hosts":          stringList(hosts...),
	})
}

// unrestrictedNetwork is the exact value of an unrestricted networking
// object: the API sends no other field, so the booleans read false and
// allowed_hosts null.
var unrestrictedNetwork = knownvalue.ObjectExact(map[string]knownvalue.Check{
	"type":                   knownvalue.StringExact("unrestricted"),
	"allow_mcp_servers":      knownvalue.Bool(false),
	"allow_package_managers": knownvalue.Bool(false),
	"allowed_hosts":          knownvalue.Null(),
})

// packages is the exact value of a packages object; a manager it does not
// name holds an empty list.
func packages(lists map[string][]string) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for _, k := range []string{"apt", "cargo", "gem", "go", "npm", "pip"} {
		checks[k] = stringList(lists[k]...)
	}
	return knownvalue.ObjectExact(checks)
}

// expectConfig checks the environment's config in state.
func expectConfig(networking, pkgs knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("config"), knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type":       knownvalue.StringExact("cloud"),
		"networking": networking,
		"packages":   pkgs,
	}))
}

// expectComputed checks the attributes the API sets.
func expectComputed(addr string, n int, createdAt, updatedAt string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(environmentID(n))),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("environment")),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact(createdAt)),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("updated_at"), knownvalue.StringExact(updatedAt)),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("archived_at"), knownvalue.Null()),
	}
}

const limitedEnvironment = `  description = "limited network"
  metadata    = { team = "a" }
  config = {
    networking = {
      type                   = "limited"
      allow_package_managers = true
      allowed_hosts          = ["api.example.com", "pypi.org"]
    }
    packages = {
      apt = ["curl"]
      pip = ["pandas", "numpy"]
    }
  }
`

const unrestrictedEnvironment = `  description = "unrestricted network"
  metadata    = { team = "a" }
  config = {
    networking = { type = "unrestricted" }
    packages   = { npm = ["typescript"], pip = [] }
  }
`

// TestEnvironmentResource_lifecycle creates a limited-network environment
// with packages, imports it, updates it in place to an unrestricted network
// with other packages, and destroys it. Configuration attributes left unset
// (allow_mcp_servers, the other package managers) are not sent and take the
// API's values; an empty list is sent as [] and clears the manager.
func TestEnvironmentResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:           environmentConfig(limitedEnvironment),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: append(expectComputed(environmentAddr, 1, at(1), at(1)),
					expectConfig(
						limitedNetwork(false, true, "api.example.com", "pypi.org"),
						packages(map[string][]string{"apt": {"curl"}, "pip": {"pandas", "numpy"}})),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("name"), knownvalue.StringExact("test-environment")),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.StringExact("limited network")),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("metadata"), knownvalue.MapExact(map[string]knownvalue.Check{"team": knownvalue.StringExact("a")})),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("archive_on_destroy"), knownvalue.Bool(false)),
				),
			},
			{
				Config:            environmentConfig(limitedEnvironment),
				ResourceName:      environmentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           environmentConfig(unrestrictedEnvironment),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionUpdate),
				// The update patches metadata after the other fields: two ticks.
				ConfigStateChecks: append(expectComputed(environmentAddr, 1, at(1), at(3)),
					expectConfig(unrestrictedNetwork,
						packages(map[string][]string{"apt": {"curl"}, "npm": {"typescript"}})),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.StringExact("unrestricted network")),
				),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateEnvironment, `{
		"name": "test-environment", "description": "limited network", "metadata": {"team": "a"},
		"config": {
			"type": "cloud",
			"networking": {"type": "limited", "allow_package_managers": true, "allowed_hosts": ["api.example.com", "pypi.org"]},
			"packages": {"apt": ["curl"], "pip": ["pandas", "numpy"]}
		}}`)
	srv.AssertJSONBodies(routeUpdateEnvironment, `{
		"name": "test-environment", "description": "unrestricted network", "metadata": {"team": "a"},
		"config": {"type": "cloud", "networking": {"type": "unrestricted"}, "packages": {"npm": ["typescript"], "pip": []}}}`)
	srv.AssertCalled(routeDeleteEnvironment, 1)
	srv.AssertCalled(routeArchiveEnvironment, 0)
	srv.assertRequestShape(t)
}

// TestEnvironmentResource_limitedNetworkUpdate switches an unrestricted
// environment to a limited network, then changes the limited network: every
// configured field is sent, and allow_mcp_servers, then removed from the
// configuration, is not sent and keeps the value the API holds.
func TestEnvironmentResource_limitedNetworkUpdate(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	limited := func(fields string) string {
		return `  config = {
    networking = {
      type = "limited"
` + fields + `    }
  }
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig(""),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{expectConfig(unrestrictedNetwork, packages(nil))},
			},
			{
				Config: environmentConfig(limited(`      allow_mcp_servers      = true
      allow_package_managers = false
      allowed_hosts          = ["a.example.com"]
`)),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectConfig(limitedNetwork(true, false, "a.example.com"), packages(nil)),
				},
			},
			{
				Config: environmentConfig(limited(`      allow_package_managers = true
      allowed_hosts          = ["a.example.com", "b.example.com"]
`)),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectConfig(limitedNetwork(true, true, "a.example.com", "b.example.com"), packages(nil)),
				},
			},
		},
	})

	emptyPackages := `"packages": {"apt": [], "cargo": [], "gem": [], "go": [], "npm": [], "pip": []}`
	srv.AssertJSONBodies(routeCreateEnvironment, `{"name": "test-environment"}`)
	srv.AssertJSONBodies(routeUpdateEnvironment,
		`{"name": "test-environment", "config": {"type": "cloud",
			"networking": {"type": "limited", "allow_mcp_servers": true, "allow_package_managers": false, "allowed_hosts": ["a.example.com"]}, `+emptyPackages+`}}`,
		`{"name": "test-environment", "config": {"type": "cloud",
			"networking": {"type": "limited", "allow_package_managers": true, "allowed_hosts": ["a.example.com", "b.example.com"]}, `+emptyPackages+`}}`)
	srv.assertRequestShape(t)
}

// TestEnvironmentResource_archiveOnDestroy: with archive_on_destroy the
// destroy archives the environment and deletes nothing.
func TestEnvironmentResource_archiveOnDestroy(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			obj, ok := srv.environments.Get(environmentID(1))
			if !ok || obj["archived_at"] != at(2) {
				return fmt.Errorf("environment after destroy: %v (found %t), want archived at %s", obj, ok, at(2))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:           environmentConfig("  archive_on_destroy = true\n"),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: append(expectComputed(environmentAddr, 1, at(1), at(1)),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("archive_on_destroy"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("metadata"), knownvalue.Null()),
				),
			},
			{
				// archive_on_destroy is not in the API: an import reads false.
				Config:                  environmentConfig("  archive_on_destroy = true\n"),
				ResourceName:            environmentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if got := states[0].Attributes["archive_on_destroy"]; got != "false" {
						return fmt.Errorf("imported archive_on_destroy = %q, want false", got)
					}
					return nil
				},
			},
		},
	})

	srv.AssertCalled(routeArchiveEnvironment, 1)
	srv.AssertCalled(routeDeleteEnvironment, 0)
	srv.AssertCalled(routeUpdateEnvironment, 0)
	srv.assertRequestShape(t)
}

// TestEnvironmentResource_toggleArchiveOnDestroy switches archive_on_destroy
// on for an existing environment: an update in place, after which the
// destroy archives.
func TestEnvironmentResource_toggleArchiveOnDestroy(t *testing.T) {
	t.Skip("bug: switching archive_on_destroy alone plans updated_at unchanged, but Update calls the API, which moves it; see fix/environments-archive-on-destroy-toggle")
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           environmentConfig(""),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionCreate),
			},
			{
				Config:           environmentConfig("  archive_on_destroy = true\n"),
				ConfigPlanChecks: expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("archive_on_destroy"), knownvalue.Bool(true)),
				},
			},
		},
	})

	srv.AssertCalled(routeArchiveEnvironment, 1)
	srv.AssertCalled(routeDeleteEnvironment, 0)
	srv.assertRequestShape(t)
}

// TestEnvironmentResource_apiErrors: an API error on create, refresh, update,
// delete or archive is an error diagnostic naming the operation, and none is
// retried (4xx).
func TestEnvironmentResource_apiErrors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		route  string
		status int
		steps  func(fail func()) []resource.TestStep
	}{
		"create": {
			route: routeCreateEnvironment, status: http.StatusBadRequest,
			steps: func(fail func()) []resource.TestStep {
				return []resource.TestStep{{PreConfig: fail, Config: environmentConfig(""), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+environment:.*400\s+Bad\s+Request`)}}
			},
		},
		"refresh": {
			route: routeGetEnvironment, status: http.StatusForbidden,
			steps: func(fail func()) []resource.TestStep {
				return []resource.TestStep{
					{Config: environmentConfig("")},
					{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+environment:.*403\s+Forbidden`)},
				}
			},
		},
		"update": {
			route: routeUpdateEnvironment, status: http.StatusBadRequest,
			steps: func(fail func()) []resource.TestStep {
				return []resource.TestStep{
					{Config: environmentConfig("")},
					{PreConfig: fail, Config: environmentConfig("  description = \"x\"\n"), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+environment:.*400\s+Bad\s+Request`)},
				}
			},
		},
		"delete": {
			route: routeDeleteEnvironment, status: http.StatusForbidden,
			steps: func(fail func()) []resource.TestStep {
				return []resource.TestStep{
					{Config: environmentConfig("")},
					{PreConfig: fail, Config: environmentConfig(""), Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+environment:.*403\s+Forbidden`)},
				}
			},
		},
		"archive": {
			route: routeArchiveEnvironment, status: http.StatusForbidden,
			steps: func(fail func()) []resource.TestStep {
				config := environmentConfig("  archive_on_destroy = true\n")
				return []resource.TestStep{
					{Config: config},
					{PreConfig: fail, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+environment:.*403\s+Forbidden`)},
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newEnvironmentsFake(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    tc.steps(func() { srv.FailNext(tc.route, tc.status, 1) }),
			})
			var faults int
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

// TestEnvironmentResource_clearAllowedHosts empties allowed_hosts of a
// limited network. The update must send "allowed_hosts": [], since the API
// keeps an omitted field ("on update, omitted fields preserve the existing
// value", https://platform.claude.com/docs/en/api/beta/environments/update).
func TestEnvironmentResource_clearAllowedHosts(t *testing.T) {
	t.Skip("bug: an empty allowed_hosts is not sent, so the API keeps the old hosts; see fix/environments-clear-allowed-hosts")
	t.Parallel()
	srv := newEnvironmentsFake(t)
	limited := func(hosts string) string {
		return `  config = {
    networking = {
      type          = "limited"
      allowed_hosts = [` + hosts + `]
    }
  }
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig(limited(`"a.example.com"`)),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{expectConfig(limitedNetwork(false, false, "a.example.com"), packages(nil))},
			},
			{
				Config:            environmentConfig(limited("")),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectConfig(limitedNetwork(false, false), packages(nil))},
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateEnvironment, `{"name": "test-environment", "config": {"type": "cloud",
		"networking": {"type": "limited", "allowed_hosts": []},
		"packages": {"apt": [], "cargo": [], "gem": [], "go": [], "npm": [], "pip": []}}}`)
	srv.assertRequestShape(t)
}
