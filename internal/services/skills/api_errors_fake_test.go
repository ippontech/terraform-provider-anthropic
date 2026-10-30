// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// TestSkillResource_apiErrors: an API error other than a 404 on refresh, on
// the version listing or on a version deletion stops the operation with the
// API's answer, and leaves the skill in the state for the next attempt. 403
// is not retried, so each fault is one request.
func TestSkillResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	files := []string{hclPath(writeBundle(t, "failing", "API errors.")[0])}
	config := skillConfig(files, "  force_destroy = true\n")
	none := providertest.ProviderConfig(sdktest.APIKey)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(skillAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetSkill, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+skill:.*403\s+Forbidden`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeListVersions, http.StatusForbidden, 1) },
				Config:      none,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+skill\s+versions\s+for\s+deletion:.*403\s+Forbidden`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteVersion, http.StatusForbidden, 1) },
				Config:      none,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+skill\s+version\s+` + version(1) + `:.*403\s+Forbidden`),
			},
		},
	})
	// The two failed destroys never reached the skill itself.
	srv.AssertCalled(routeDeleteSkill, 1)
}

// TestSkillVersionResource_apiErrors: the same for a version: a refresh
// error and a deletion error other than 404 reach the operator.
func TestSkillVersionResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newSkillsFake(t, 0)
	skillFiles := hclPaths(writeBundle(t, "pilot-skill", "First upload."))
	v2 := hclPaths(writeBundle(t, "pilot-skill", "Second upload."))
	config := skillAndVersionConfig(skillFiles, v2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             func(*terraform.State) error { return srv.checkEmpty() },
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(versionAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetVersion, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+skill\s+version:.*403\s+Forbidden`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteVersion, http.StatusForbidden, 1) },
				Config:      skillConfig(skillFiles, "  force_destroy = true\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+skill\s+version:.*403\s+Forbidden`),
			},
		},
	})
}
