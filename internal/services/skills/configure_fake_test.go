// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/skills"
)

// TestSkills_requireAPIKey: every resource and data source of the package
// refuses to run with an Admin key alone, before any request.
func TestSkills_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_skill resource":         `resource "anthropic_skill" "test" { files = ["x/SKILL.md"] }`,
		"anthropic_skill_version resource": "resource \"anthropic_skill_version\" \"test\" {\n  skill_id = \"skill_x\"\n  files = [\"x/SKILL.md\"]\n}",
		"anthropic_skill":                  `data "anthropic_skill" "test" { skill_id = "skill_x" }`,
		"anthropic_skills":                 `data "anthropic_skills" "test" {}`,
		"anthropic_skill_version":          "data \"anthropic_skill_version\" \"test\" {\n  skill_id = \"skill_x\"\n  version = \"1\"\n}",
		"anthropic_skill_versions":         `data "anthropic_skill_versions" "test" { skill_id = "skill_x" }`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newSkillsFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestSkills_rejectForeignProviderData covers the Configure guard Terraform
// cannot reach: provider data of another type is an error.
func TestSkills_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_skill":         skills.NewSkillResource(),
		"anthropic_skill_version": skills.NewSkillVersionResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_skill":          skills.NewSkillDataSource(),
		"anthropic_skills":         skills.NewSkillsDataSource(),
		"anthropic_skill_version":  skills.NewSkillVersionDataSource(),
		"anthropic_skill_versions": skills.NewSkillVersionsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
