// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

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

const environmentsAddr = "data.anthropic_environments.test"

func environmentsConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_environments" "test" {
` + extra + `}
`
}

// seedFour stores four environments, the second of them archived.
func seedFour(srv *environmentsFake) {
	seedDefault(srv, "env_a", "a")
	seedLimited(srv, "env_archived")
	seedDefault(srv, "env_b", "b")
	seedDefault(srv, "env_c", "c")
}

// TestEnvironmentsDataSource_pages lists the environments two per page: the
// data source follows next_page with page=, keeps the API's order, and
// without include_archived sends none, so the API leaves the archived one
// out.
func TestEnvironmentsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t, 2)
	seedFour(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: environmentsConfig(""),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(environmentsAddr, tfjsonpath.New("include_archived"), knownvalue.Null()),
				statecheck.ExpectKnownValue(environmentsAddr, tfjsonpath.New("environments"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(defaultObject("env_a", "a")),
					knownvalue.ObjectExact(defaultObject("env_b", "b")),
					knownvalue.ObjectExact(defaultObject("env_c", "c")),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListEnvironments, "beta=true", "beta=true&page="+sdktest.NextPageToken("env_b"))
	srv.AssertAllPagesRead(routeListEnvironments)
	srv.assertRequestShape(t)
}

// TestEnvironmentsDataSource_includeArchived sends include_archived as
// configured: true lists the archived environment too, false is sent as is.
func TestEnvironmentsDataSource_includeArchived(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		value string
		walk  []string
		want  []knownvalue.Check
	}{
		"true": {
			value: "true",
			walk: []string{
				"beta=true&include_archived=true",
				"beta=true&include_archived=true&page=" + sdktest.NextPageToken("env_archived"),
			},
			want: []knownvalue.Check{
				knownvalue.ObjectExact(defaultObject("env_a", "a")),
				knownvalue.ObjectExact(limitedObject("env_archived")),
				knownvalue.ObjectExact(defaultObject("env_b", "b")),
				knownvalue.ObjectExact(defaultObject("env_c", "c")),
			},
		},
		"false": {
			value: "false",
			walk: []string{
				"beta=true&include_archived=false",
				"beta=true&include_archived=false&page=" + sdktest.NextPageToken("env_b"),
			},
			want: []knownvalue.Check{
				knownvalue.ObjectExact(defaultObject("env_a", "a")),
				knownvalue.ObjectExact(defaultObject("env_b", "b")),
				knownvalue.ObjectExact(defaultObject("env_c", "c")),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newEnvironmentsFake(t, 2)
			seedFour(srv)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config: environmentsConfig("  include_archived = " + tc.value + "\n"),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(environmentsAddr, tfjsonpath.New("environments"), knownvalue.ListExact(tc.want)),
					},
				}},
			})
			srv.AssertWalks(routeListEnvironments, tc.walk...)
			srv.AssertAllPagesRead(routeListEnvironments)
		})
	}
}

// TestEnvironmentsDataSource_empty: no environment is an empty list, never
// null.
func TestEnvironmentsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: environmentsConfig(""),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(environmentsAddr, tfjsonpath.New("environments"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListEnvironments, "beta=true")
}

// TestEnvironmentsDataSource_error: an API error is an error diagnostic.
func TestEnvironmentsDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	srv.FailNext(routeListEnvironments, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      environmentsConfig(""),
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+environments:.*403\s+Forbidden`),
		}},
	})
	srv.AssertCalled(routeListEnvironments, 1)
}
