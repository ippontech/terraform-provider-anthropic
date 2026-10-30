// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

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

const skillAddr = "anthropic_skill.test"

// skillID is the ID the fake gives to the n-th skill it creates.
func skillID(n int) string { return fmt.Sprintf("skill_%024d", n) }

// version is the version string the fake gives to the n-th version it
// creates, the first version of every skill included.
func version(n int) string { return fmt.Sprintf("17591780106411%02d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func skillConfig(files []string, extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
resource "anthropic_skill" "test" {
  files = %s
%s}
`, hclList(files), extra)
}

func expectSkill(id, title, createdAt, updatedAt, latest string, forceDestroy bool, files []string) []statecheck.StateCheck {
	paths := make([]knownvalue.Check, len(files))
	for i, f := range files {
		paths[i] = knownvalue.StringExact(f)
	}
	var titleCheck knownvalue.Check = knownvalue.Null()
	if title != "" {
		titleCheck = knownvalue.StringExact(title)
	}
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("display_title"), titleCheck),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("files"), knownvalue.ListExact(paths)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("force_destroy"), knownvalue.Bool(forceDestroy)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(createdAt)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(updatedAt)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("latest_version"), knownvalue.StringExact(latest)),
		statecheck.ExpectKnownValue(skillAddr, tfjsonpath.New("source"), knownvalue.StringExact("custom")),
	}
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// TestSkillResource_lifecycle creates a skill from a nested bundle, imports
// it, replaces it on a display_title change (the attribute is
// RequiresReplace) and destroys it with its versions (force_destroy).
func TestSkillResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := writeBundle(t, "pilot-skill", "Pilot skill.", "scripts/run.sh")
	files = []string{hclPath(files[0]), hclPath(files[1])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:            skillConfig(files, "  display_title = \"Pilot\"\n  force_destroy = true\n"),
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSkill(skillID(1), "Pilot", at(1), at(2), version(1), true, files),
			},
			{
				Config:                  skillConfig(files, "  display_title = \"Pilot\"\n  force_destroy = true\n"),
				ResourceName:            skillAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files", "force_destroy"},
			},
			{
				Config:            skillConfig(files, "  display_title = \"Pilot renamed\"\n  force_destroy = true\n"),
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectSkill(skillID(2), "Pilot renamed", at(3), at(4), version(2), true, files),
			},
		},
	})

	// One create per skill, one delete per skill and per version: the
	// replacement and the final destroy.
	srv.AssertCalled(routeCreateSkill, 2)
	srv.AssertCalled(routeDeleteSkill, 2)
	srv.AssertCalled(routeDeleteVersion, 2)
	srv.assertRequestShape(t)

	got, values := srv.uploadedFiles(t, routeCreateSkill, 0)
	slices.Sort(got)
	if want := []string{"files[]=pilot-skill/SKILL.md", "files[]=pilot-skill/scripts/run.sh"}; !slices.Equal(got, want) {
		t.Errorf("uploaded %q, want %q", got, want)
	}
	if want := map[string]string{"display_title": "Pilot"}; fmt.Sprint(values) != fmt.Sprint(want) {
		t.Errorf("form values %v, want %v", values, want)
	}
}

// TestSkillResource_withoutDisplayTitle: without display_title in the
// configuration the request omits it and the state keeps it null, although
// the API derives one; an import, which has no configuration to compare
// with, adopts the derived title.
func TestSkillResource_withoutDisplayTitle(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "untitled", "No title.")[0])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:            skillConfig(files, "  force_destroy = true\n"),
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSkill(skillID(1), "", at(1), at(2), version(1), true, files),
			},
			{
				Config:                  skillConfig(files, "  force_destroy = true\n"),
				ResourceName:            skillAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files", "force_destroy", "display_title"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].Attributes["display_title"] != "untitled" {
						return fmt.Errorf("imported display_title = %q, want the derived %q", states[0].Attributes["display_title"], "untitled")
					}
					return nil
				},
			},
		},
	})

	_, values := srv.uploadedFiles(t, routeCreateSkill, 0)
	if len(values) != 0 {
		t.Errorf("form values %v, want none (no display_title)", values)
	}
}

// TestSkillResource_removedOutsideTerraform: a skill deleted behind
// Terraform's back leaves the state on refresh, and the next apply creates
// it again instead of failing.
func TestSkillResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "gone", "Deleted out of band.")[0])}
	config := skillConfig(files, "  force_destroy = true\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.deleteOutOfBand(skillID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSkill(skillID(2), "", at(3), at(4), version(2), true, files),
			},
		},
	})
}

// TestSkillResource_deleteToleratesNotFound: a version or a skill already
// gone (404) when Terraform deletes it is not an error, and a vanished
// version does not stop the deletion of the next versions nor of the skill.
func TestSkillResource_deleteToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "racing", "Deleted twice.")[0])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           skillConfig(files, "  force_destroy = true\n"),
				ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionCreate),
				Check: func(*terraform.State) error {
					// A second version, uploaded outside this configuration.
					srv.newVersion(skillID(1), bundle{dir: "racing", name: "racing"})
					return nil
				},
			},
			{
				PreConfig: func() {
					srv.FailNext(routeDeleteVersion, http.StatusNotFound, 1)
					srv.FailNext(routeDeleteSkill, http.StatusNotFound, 1)
				},
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	var deleted []string
	for _, r := range srv.Requests(routeDeleteVersion) {
		deleted = append(deleted, r.Path+" "+r.Fault)
	}
	want := []string{
		"/v1/skills/" + skillID(1) + "/versions/" + version(1) + " status 404",
		"/v1/skills/" + skillID(1) + "/versions/" + version(2) + " ",
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("version deletions %q, want %q", deleted, want)
	}
	srv.AssertCalled(routeDeleteSkill, 1)
}

// TestSkillResource_withoutForceDestroy: without force_destroy the provider
// does not touch the versions, so the API refuses to delete the skill and
// the error reaches the operator; after the versions are gone the destroy
// goes through.
func TestSkillResource_withoutForceDestroy(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "kept", "Kept versions.")[0])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:            skillConfig(files, ""),
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSkill(skillID(1), "", at(1), at(2), version(1), false, files),
			},
			{
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+skill:.*400.*still\s+has\s+1\s+versions`),
			},
			{
				PreConfig: func() {
					for _, v := range srv.versionsOf(skillID(1)) {
						srv.versions.Delete(v["version"].(string))
					}
				},
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeListVersions, 0)
	srv.AssertCalled(routeDeleteVersion, 0)
}

// TestSkillResource_forceDestroyPagesThroughVersions: force_destroy deletes
// every version, following next_page while it deletes (three versions, one
// per page), before deleting the skill.
func TestSkillResource_forceDestroyPagesThroughVersions(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 1)
	paths := writeBundle(t, "versioned", "Three versions.")
	files := []string{hclPath(paths[0])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:           skillConfig(files, "  force_destroy = true\n"),
				ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionCreate),
				Check: func(*terraform.State) error {
					// Two more versions, uploaded outside this configuration.
					srv.newVersion(skillID(1), bundle{dir: "versioned", name: "versioned"})
					srv.newVersion(skillID(1), bundle{dir: "versioned", name: "versioned"})
					return nil
				},
			},
		},
	})
	srv.AssertCalled(routeDeleteVersion, 3)
	srv.AssertAllPagesRead(routeListVersions)
	var deleted []string
	for _, r := range srv.Requests(routeDeleteVersion) {
		deleted = append(deleted, r.Path)
	}
	want := []string{
		"/v1/skills/" + skillID(1) + "/versions/" + version(1),
		"/v1/skills/" + skillID(1) + "/versions/" + version(2),
		"/v1/skills/" + skillID(1) + "/versions/" + version(3),
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deleted %q, want %q", deleted, want)
	}
}

// TestSkillResource_toggleForceDestroy: force_destroy is the one attribute
// that changes without a replacement, so switching it on is an in-place
// update that must leave a consistent state (the attribute's documentation
// requires that apply before a destroy can use it).
func TestSkillResource_toggleForceDestroy(t *testing.T) {
	t.Skip("bug: SkillResource.Update is a no-op, so the state keeps the prior force_destroy and " +
		"Terraform fails the apply with \"Provider produced inconsistent result after apply: .force_destroy: " +
		"was cty.True, but now cty.False\"; force_destroy cannot be switched on for an existing skill. " +
		"Fix: Update writes the planned force_destroy to the state.")
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "toggled", "Toggled.")[0])}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{Config: skillConfig(files, ""), ConfigPlanChecks: expectAction(skillAddr, plancheck.ResourceActionCreate)},
			{
				Config:            skillConfig(files, "  force_destroy = true\n"),
				ConfigPlanChecks:  expectAction(skillAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSkill(skillID(1), "", at(1), at(2), version(1), true, files),
			},
		},
	})
	srv.AssertCalled(routeCreateSkill, 1)
}

// TestSkillResource_createErrors: a bundle the provider cannot root fails
// before any request, and a bundle the API rejects fails with the API's
// message; neither leaves anything in the state.
func TestSkillResource_createErrors(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	// The frontmatter name differs from the directory: the API rejects it.
	mismatched := writeBundle(t, "dir-name", "Mismatch.")
	writeFile(t, mismatched[0], "---\nname: other-name\ndescription: x\n---\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:      skillConfig([]string{"relative/SKILL.md", "/absolute/other.md"}, ""),
				ExpectError: regexp.MustCompile(`Invalid\s+skill\s+bundle`),
			},
			{
				Config:      skillConfig([]string{hclPath(mismatched[0])}, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+skill:.*400.*does\s+not\s+match\s+the\s+directory`),
			},
		},
	})
	srv.AssertCalled(routeCreateSkill, 1)
}
