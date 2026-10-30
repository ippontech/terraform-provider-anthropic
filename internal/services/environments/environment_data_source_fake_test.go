// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// seedLimited stores, outside Terraform, an archived environment with a
// limited network, packages, a description and metadata, as the create
// reference's example response shows it
// (https://platform.claude.com/docs/en/api/beta/environments/create).
func seedLimited(f *environmentsFake, id string) {
	f.environments.Put(map[string]any{
		"type": "environment", "id": id, "name": "limited-env", "description": "Data analysis.",
		"metadata": map[string]any{"team": "data"},
		"config": map[string]any{
			"type": "cloud",
			"networking": map[string]any{
				"type": "limited", "allow_mcp_servers": false, "allow_package_managers": true,
				"allowed_hosts": []any{"api.example.com"},
			},
			"packages": map[string]any{
				"type": "packages", "apt": []any{"curl"}, "cargo": []any{"ripgrep"}, "gem": []any{"rake"},
				"go": []any{"golang.org/x/tools"}, "npm": []any{"typescript"}, "pip": []any{"pandas", "numpy"},
			},
		},
		"created_at": at(1), "updated_at": at(2), "archived_at": at(3),
	})
}

// seedDefault stores an environment with what create fills in by default:
// no description (null), metadata {}, an unrestricted network and no
// packages.
func seedDefault(f *environmentsFake, id, name string) {
	f.environments.Put(map[string]any{
		"type": "environment", "id": id, "name": name, "description": nil, "metadata": map[string]any{},
		"config":     applyConfig(nil, nil),
		"created_at": at(4), "updated_at": at(5), "archived_at": nil,
	})
}

// limitedObject and defaultObject are the exact values both data sources
// give the seeded environments: a null description reads "", empty metadata
// {} (unlike the resource, which maps both to null).
var (
	limitedConfig = knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type":       knownvalue.StringExact("cloud"),
		"networking": limitedNetwork(false, true, "api.example.com"),
		"packages": packages(map[string][]string{
			"apt": {"curl"}, "cargo": {"ripgrep"}, "gem": {"rake"}, "go": {"golang.org/x/tools"},
			"npm": {"typescript"}, "pip": {"pandas", "numpy"},
		}),
	})
	defaultConfig = knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type":       knownvalue.StringExact("cloud"),
		"networking": unrestrictedNetwork,
		"packages":   packages(nil),
	})
)

func limitedObject(id string) map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"id":          knownvalue.StringExact(id),
		"name":        knownvalue.StringExact("limited-env"),
		"description": knownvalue.StringExact("Data analysis."),
		"metadata":    knownvalue.MapExact(map[string]knownvalue.Check{"team": knownvalue.StringExact("data")}),
		"config":      limitedConfig,
		"created_at":  knownvalue.StringExact(at(1)),
		"updated_at":  knownvalue.StringExact(at(2)),
		"archived_at": knownvalue.StringExact(at(3)),
		"type":        knownvalue.StringExact("environment"),
	}
}

func defaultObject(id, name string) map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"id":          knownvalue.StringExact(id),
		"name":        knownvalue.StringExact(name),
		"description": knownvalue.StringExact(""),
		"metadata":    knownvalue.MapExact(map[string]knownvalue.Check{}),
		"config":      defaultConfig,
		"created_at":  knownvalue.StringExact(at(4)),
		"updated_at":  knownvalue.StringExact(at(5)),
		"archived_at": knownvalue.Null(),
		"type":        knownvalue.StringExact("environment"),
	}
}

// expectAttributes checks every attribute of addr against want.
func expectAttributes(addr string, want map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(want))
	for name, check := range want {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), check))
	}
	return checks
}

// TestEnvironmentDataSource_read reads two environments by ID: every
// attribute comes from the response.
func TestEnvironmentDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	seedLimited(srv, "env_limited")
	seedDefault(srv, "env_default", "default-env")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_environment" "limited" {
  environment_id = "env_limited"
}

data "anthropic_environment" "default" {
  environment_id = "env_default"
}
`,
			ConfigStateChecks: append(
				expectAttributes("data.anthropic_environment.limited", limitedObject("env_limited")),
				expectAttributes("data.anthropic_environment.default", defaultObject("env_default", "default-env"))...),
		}},
	})

	for _, r := range srv.Requests(routeGetEnvironment) {
		if r.Path != "/v1/environments/env_limited" && r.Path != "/v1/environments/env_default" {
			t.Errorf("GET %s, want one of the two configured environments", r.Path)
		}
	}
	srv.assertRequestShape(t)
}

// TestEnvironmentDataSource_notFound: an unknown environment is an error
// diagnostic.
func TestEnvironmentDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_environment" "test" {
  environment_id = "env_missing"
}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+environment:.*404\s+Not\s+Found`),
		}},
	})
	srv.AssertCalled(routeGetEnvironment, 1)
}
