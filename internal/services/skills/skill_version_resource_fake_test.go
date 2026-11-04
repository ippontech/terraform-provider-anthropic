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

const versionAddr = "anthropic_skill_version.test"

// versionID is the ID the fake gives to the n-th version it creates.
func versionID(n int) string { return fmt.Sprintf("skillver_%024d", n) }

// skillAndVersionConfig declares a skill (force_destroy, so its first
// version goes with it) and one version of it uploaded from versionFiles.
func skillAndVersionConfig(skillFiles, versionFiles []string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
resource "anthropic_skill" "test" {
  files         = %s
  force_destroy = true
}

resource "anthropic_skill_version" "test" {
  skill_id = anthropic_skill.test.id
  files    = %s
}
`, hclList(skillFiles), hclList(versionFiles))
}

func expectVersion(n int, description, createdAt string, files []string) []statecheck.StateCheck {
	paths := make([]knownvalue.Check, len(files))
	for i, f := range files {
		paths[i] = knownvalue.StringExact(f)
	}
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("id"), knownvalue.StringExact(versionID(n))),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("skill_id"), knownvalue.StringExact(skillID(1))),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("version"), knownvalue.StringExact(version(n))),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("name"), knownvalue.StringExact("pilot-skill")),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("description"), knownvalue.StringExact(description)),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("directory"), knownvalue.StringExact("pilot-skill")),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(createdAt)),
		statecheck.ExpectKnownValue(versionAddr, tfjsonpath.New("files"), knownvalue.ListExact(paths)),
	}
}

func hclPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = hclPath(p)
	}
	return out
}

// TestSkillVersionResource_lifecycle uploads a version of a skill, imports
// it by <skill_id>/<version>, replaces it when its files change (files is
// RequiresReplace) without touching the skill, and destroys both.
func TestSkillVersionResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	skillFiles := hclPaths(writeBundle(t, "pilot-skill", "First upload."))
	v2 := hclPaths(writeBundle(t, "pilot-skill", "Second upload."))
	v3 := hclPaths(writeBundle(t, "pilot-skill", "Third upload.", "reference/notes.md"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:            skillAndVersionConfig(skillFiles, v2),
				ConfigPlanChecks:  expectAction(versionAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectVersion(2, "Second upload.", at(2), v2),
			},
			{
				Config:            skillAndVersionConfig(skillFiles, v2),
				ResourceName:      versionAddr,
				ImportState:       true,
				ImportStateIdFunc: importVersionID,
				ImportStateVerify: true,
				// The API does not return the local paths.
				ImportStateVerifyIgnore: []string{"files"},
			},
			{
				Config: skillAndVersionConfig(skillFiles, v3),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(versionAddr, plancheck.ResourceActionReplace),
						plancheck.ExpectResourceAction(skillAddr, plancheck.ResourceActionNoop),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: expectVersion(3, "Third upload.", at(3), v3),
			},
		},
	})

	srv.AssertCalled(routeCreateSkill, 1)
	srv.AssertCalled(routeCreateVersion, 2)
	// v2 on replacement, v3 on destroy, v1 by the skill's force_destroy.
	srv.AssertCalled(routeDeleteVersion, 3)
	srv.AssertCalled(routeDeleteSkill, 1)
	srv.assertRequestShape(t)

	for i, want := range [][]string{
		{"files[]=pilot-skill/SKILL.md"},
		{"files[]=pilot-skill/SKILL.md", "files[]=pilot-skill/reference/notes.md"},
	} {
		got, fields := srv.uploadedFiles(t, routeCreateVersion, i)
		slices.Sort(got)
		if !slices.Equal(got, want) || len(fields) != 0 {
			t.Errorf("version upload %d sent files %q and fields %v, want %q and no field", i+1, got, fields, want)
		}
		if p := srv.Requests(routeCreateVersion)[i].Path; p != "/v1/skills/"+skillID(1)+"/versions" {
			t.Errorf("version upload %d went to %s", i+1, p)
		}
	}
}

func importVersionID(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources[versionAddr]
	if !ok {
		return "", fmt.Errorf("%s not in state", versionAddr)
	}
	return rs.Primary.Attributes["skill_id"] + "/" + rs.Primary.Attributes["version"], nil
}

// TestSkillVersionResource_importRejectsMalformedID: the import ID must be
// <skill_id>/<version>, both parts non-empty.
func TestSkillVersionResource_importRejectsMalformedID(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	skillFiles := hclPaths(writeBundle(t, "pilot-skill", "First upload."))
	v2 := hclPaths(writeBundle(t, "pilot-skill", "Second upload."))
	config := skillAndVersionConfig(skillFiles, v2)

	steps := []resource.TestStep{{Config: config, ConfigPlanChecks: expectAction(versionAddr, plancheck.ResourceActionCreate)}}
	for _, id := range []string{"no-separator", "/" + version(2), skillID(1) + "/"} {
		steps = append(steps, resource.TestStep{
			Config:        config,
			ResourceName:  versionAddr,
			ImportState:   true,
			ImportStateId: id,
			ExpectError:   regexp.MustCompile(`(?s)Invalid\s+import\s+ID.*Expected\s+format:\s+<skill_id>/<version>`),
		})
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps:                    steps,
	})
}

// TestSkillVersionResource_removedOutsideTerraform: a version deleted
// behind Terraform's back leaves the state on refresh and is uploaded again.
func TestSkillVersionResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	skillFiles := hclPaths(writeBundle(t, "pilot-skill", "First upload."))
	v2 := hclPaths(writeBundle(t, "pilot-skill", "Second upload."))
	config := skillAndVersionConfig(skillFiles, v2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(versionAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.versions.Delete(version(2)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(versionAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectVersion(3, "Second upload.", at(3), v2),
			},
		},
	})
}

// TestSkillVersionResource_deleteToleratesNotFound: a version already gone
// when Terraform deletes it is not an error.
func TestSkillVersionResource_deleteToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	skillFiles := hclPaths(writeBundle(t, "pilot-skill", "First upload."))
	v2 := hclPaths(writeBundle(t, "pilot-skill", "Second upload."))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:           skillAndVersionConfig(skillFiles, v2),
				ConfigPlanChecks: expectAction(versionAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteVersion, http.StatusNotFound, 1) },
				Config:           skillConfig(skillFiles, "  force_destroy = true\n"),
				ConfigPlanChecks: expectAction(versionAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	// The injected 404, then v1 and the still stored v2 on the final destroy.
	srv.AssertCalled(routeDeleteVersion, 3)
}

// TestSkillVersionResource_createErrors: a bundle the provider cannot root
// fails before any request; a version of a missing skill fails with the
// API's 404.
func TestSkillVersionResource_createErrors(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	v := hclPaths(writeBundle(t, "pilot-skill", "Orphan."))
	config := func(files []string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
resource "anthropic_skill_version" "test" {
  skill_id = "skill_missing"
  files    = %s
}
`, hclList(files))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{
				Config:      config([]string{"relative/SKILL.md", "/absolute/other.md"}),
				ExpectError: regexp.MustCompile(`Invalid\s+skill\s+bundle`),
			},
			{
				Config:      config(v),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+skill\s+version:.*404.*skill\s+skill_missing\s+not\s+found`),
			},
		},
	})
	srv.AssertCalled(routeCreateVersion, 1)
}
