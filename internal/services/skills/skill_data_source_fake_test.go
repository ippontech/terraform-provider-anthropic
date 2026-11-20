// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

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

// seedSkill stores a skill as the API returns it, outside Terraform.
func seedSkill(f *skillsFake, id, title, source string, n int) map[string]any {
	s := map[string]any{
		"type":           "skill",
		"id":             id,
		"display_title":  title,
		"source":         source,
		"latest_version": version(n),
		"created_at":     at(n),
		"updated_at":     at(n + 1),
	}
	f.skills.Put(s)
	return s
}

// skillObject is the exact state value of a skills[*] element for a seeded
// skill.
func skillObject(s map[string]any) knownvalue.Check {
	title := knownvalue.Check(knownvalue.Null())
	if t := s["display_title"].(string); t != "" {
		title = knownvalue.StringExact(t)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":             knownvalue.StringExact(s["id"].(string)),
		"display_title":  title,
		"created_at":     knownvalue.StringExact(s["created_at"].(string)),
		"updated_at":     knownvalue.StringExact(s["updated_at"].(string)),
		"latest_version": knownvalue.StringExact(s["latest_version"].(string)),
		"source":         knownvalue.StringExact(s["source"].(string)),
		"type":           knownvalue.StringExact("skill"),
	})
}

// TestSkillDataSource_read reads two skills by ID: every attribute comes
// from the response, and an empty display_title is null.
func TestSkillDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	seedSkill(srv, "skill_titled", "Titled", "custom", 3)
	seedSkill(srv, "skill_untitled", "", "anthropic", 5)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skill" "titled" {
  skill_id = "skill_titled"
}

data "anthropic_skill" "untitled" {
  skill_id = "skill_untitled"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("skill_id"), knownvalue.StringExact("skill_titled")),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("id"), knownvalue.StringExact("skill_titled")),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("display_title"), knownvalue.StringExact("Titled")),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("created_at"), knownvalue.StringExact(at(3))),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("updated_at"), knownvalue.StringExact(at(4))),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("latest_version"), knownvalue.StringExact(version(3))),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("source"), knownvalue.StringExact("custom")),
				statecheck.ExpectKnownValue("data.anthropic_skill.titled", tfjsonpath.New("type"), knownvalue.StringExact("skill")),
				statecheck.ExpectKnownValue("data.anthropic_skill.untitled", tfjsonpath.New("display_title"), knownvalue.Null()),
				statecheck.ExpectKnownValue("data.anthropic_skill.untitled", tfjsonpath.New("source"), knownvalue.StringExact("anthropic")),
				statecheck.ExpectKnownValue("data.anthropic_skill.untitled", tfjsonpath.New("latest_version"), knownvalue.StringExact(version(5))),
			},
		}},
	})
	srv.AssertWalks(routeGetSkill, "beta=true")
	srv.assertRequestShape(t)
}

// TestSkillDataSource_notFound: an unknown skill is an error diagnostic.
func TestSkillDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skill" "test" {
  skill_id = "skill_missing"
}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+skill:.*404\s+Not\s+Found`),
		}},
	})
}

// TestSkillsDataSource_sourceFilter lists custom skills only, two per page:
// the filter goes to the API as source=custom, the data source follows
// next_page with page= and keeps the API's order.
func TestSkillsDataSource_sourceFilter(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 2)
	c1 := seedSkill(srv, "skill_c1", "Custom 1", "custom", 1)
	seedSkill(srv, "skill_a1", "Anthropic 1", "anthropic", 3)
	c2 := seedSkill(srv, "skill_c2", "", "custom", 5)
	seedSkill(srv, "skill_a2", "Anthropic 2", "anthropic", 7)
	c3 := seedSkill(srv, "skill_c3", "Custom 3", "custom", 9)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skills" "test" {
  source_filter = "custom"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skills.test", tfjsonpath.New("skills"),
					knownvalue.ListExact([]knownvalue.Check{skillObject(c1), skillObject(c2), skillObject(c3)})),
			},
		}},
	})
	srv.AssertWalks(routeListSkills,
		"beta=true&source=custom",
		"beta=true&page="+sdktest.NextPageToken("skill_c2")+"&source=custom")
	srv.AssertAllPagesRead(routeListSkills)
	srv.assertRequestShape(t)
}

// TestSkillsDataSource_noFilter: without source_filter no source is sent and
// every skill is listed.
func TestSkillsDataSource_noFilter(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	c1 := seedSkill(srv, "skill_c1", "Custom 1", "custom", 1)
	a1 := seedSkill(srv, "skill_a1", "Anthropic 1", "anthropic", 3)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skills" "test" {}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skills.test", tfjsonpath.New("skills"),
					knownvalue.ListExact([]knownvalue.Check{skillObject(c1), skillObject(a1)})),
			},
		}},
	})
	srv.AssertWalks(routeListSkills, "beta=true")
}

// TestSkillsDataSource_empty: no skill is an empty list, never null.
func TestSkillsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skills" "test" {}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_skills.test", tfjsonpath.New("skills"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// TestSkillsDataSource_error: an API error is an error diagnostic.
func TestSkillsDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	srv.FailNext(routeListSkills, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_skills" "test" {}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+skills:.*403\s+Forbidden`),
		}},
	})
	srv.AssertCalled(routeListSkills, 1)
}
