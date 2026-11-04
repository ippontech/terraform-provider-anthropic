// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"fmt"
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

// seedVersion stores the n-th version of a skill as the API returns it.
func seedVersion(f *skillsFake, skill string, n int) map[string]any {
	v := map[string]any{
		"type":        "skill_version",
		"id":          versionID(n),
		"skill_id":    skill,
		"version":     version(n),
		"name":        fmt.Sprintf("name-%d", n),
		"description": fmt.Sprintf("Description %d.", n),
		"directory":   fmt.Sprintf("dir-%d", n),
		"created_at":  at(n),
	}
	f.versions.Put(v)
	return v
}

// versionObject is the exact state value of a versions[*] element.
func versionObject(v map[string]any) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for _, k := range []string{"id", "version", "name", "description", "directory", "created_at", "type"} {
		checks[k] = knownvalue.StringExact(v[k].(string))
	}
	return knownvalue.ObjectExact(checks)
}

// TestSkillVersionDataSource_read reads a version by skill ID and version:
// the request names both, and every attribute comes from the response.
func TestSkillVersionDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	seedSkill(srv, "skill_one", "One", "custom", 1)
	seedVersion(srv, "skill_one", 1)
	seedVersion(srv, "skill_one", 2)

	const addr = "data.anthropic_skill_version.test"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
data "anthropic_skill_version" "test" {
  skill_id = "skill_one"
  version  = %q
}
`, version(2)),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("skill_id"), knownvalue.StringExact("skill_one")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("version"), knownvalue.StringExact(version(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(versionID(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact("name-2")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("description"), knownvalue.StringExact("Description 2.")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("directory"), knownvalue.StringExact("dir-2")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("skill_version")),
			},
		}},
	})
	srv.AssertWalks(routeGetVersion, "beta=true")
	for _, r := range srv.Requests(routeGetVersion) {
		if r.Path != "/v1/skills/skill_one/versions/"+version(2) {
			t.Errorf("GET %s, want the version under its skill", r.Path)
		}
	}
	srv.assertRequestShape(t)
}

// TestSkillVersionDataSource_notFound: a version of another skill is not
// found, and that is an error diagnostic.
func TestSkillVersionDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	seedVersion(srv, "skill_other", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
data "anthropic_skill_version" "test" {
  skill_id = "skill_one"
  version  = %q
}
`, version(1)),
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+skill\s+version:.*404\s+Not\s+Found`),
		}},
	})
}

const versionsConfig = `
data "anthropic_skill_versions" "test" {
  skill_id = "skill_one"
}
`

// TestSkillVersionsDataSource_pages lists the three versions of one skill,
// two per page, and none of another skill's.
func TestSkillVersionsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 2)
	v1 := seedVersion(srv, "skill_one", 1)
	seedVersion(srv, "skill_two", 2)
	v3 := seedVersion(srv, "skill_one", 3)
	v4 := seedVersion(srv, "skill_one", 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + versionsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skill_versions.test", tfjsonpath.New("skill_id"), knownvalue.StringExact("skill_one")),
				statecheck.ExpectKnownValue("data.anthropic_skill_versions.test", tfjsonpath.New("versions"),
					knownvalue.ListExact([]knownvalue.Check{versionObject(v1), versionObject(v3), versionObject(v4)})),
			},
		}},
	})
	srv.AssertWalks(routeListVersions, "beta=true", "beta=true&page="+sdktest.NextPageToken(versionID(3)))
	srv.AssertAllPagesRead(routeListVersions)
	srv.assertRequestShape(t)
}

// TestSkillVersionsDataSource_empty: a skill without versions is an empty
// list, never null.
func TestSkillVersionsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + versionsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skill_versions.test", tfjsonpath.New("versions"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// TestSkillVersionsDataSource_error: an API error is an error diagnostic.
func TestSkillVersionsDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	srv.FailNext(routeListVersions, http.StatusNotFound, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.APIKey) + versionsConfig,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+skill\s+versions:.*404\s+Not\s+Found`),
		}},
	})
	srv.AssertCalled(routeListVersions, 1)
}
