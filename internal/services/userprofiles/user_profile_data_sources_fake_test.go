// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/userprofiles"
)

// threeProfiles stores three profiles; the first has every field and an
// active trust grant.
func (f *profilesFake) threeProfiles() {
	p := f.newProfile("passthrough", "ext-1", "Acme", map[string]any{"tier": "gold"})
	p["trust_grants"] = map[string]any{"zero_data_retention": map[string]any{"status": "active"}}
	f.profiles.Put(p)
	f.newProfile(nil, nil, nil, nil)
	f.newProfile(nil, "ext-3", nil, nil)
}

// profileObject is the exact data source view of the n-th profile of
// threeProfiles: null metadata and trust_grants when empty.
func profileObject(n int) map[string]knownvalue.Check {
	o := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(profileID(n)), "access_type": knownvalue.StringExact("application"),
		"external_id": knownvalue.Null(), "name": knownvalue.Null(), "metadata": knownvalue.Null(), "trust_grants": knownvalue.Null(),
		"type": knownvalue.StringExact("user_profile"), "created_at": knownvalue.StringExact(at(n)), "updated_at": knownvalue.StringExact(at(n)),
	}
	switch n {
	case 1:
		o["access_type"], o["external_id"], o["name"] = knownvalue.StringExact("passthrough"), knownvalue.StringExact("ext-1"), knownvalue.StringExact("Acme")
		o["metadata"], o["trust_grants"] = stringMap(map[string]string{"tier": "gold"}), grants(map[string]string{"zero_data_retention": "active"})
	case 3:
		o["external_id"] = knownvalue.StringExact("ext-3")
	}
	return o
}

func dataChecks(addr string, attrs map[string]knownvalue.Check) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for name, c := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), c))
	}
	return checks
}

// TestUserProfileDataSource_read reads a full profile and a bare one; an
// unknown ID is an error.
func TestUserProfileDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 0)
	srv.threeProfiles()
	const addr = "data.anthropic_user_profile.test"
	block := func(id string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_user_profile" "test" { id = "` + id + `" }`
	}

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{Config: block(profileID(1)), ConfigStateChecks: dataChecks(addr, profileObject(1))},
			{Config: block(profileID(2)), ConfigStateChecks: dataChecks(addr, profileObject(2))},
			{Config: block("uprof_missing"), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+user\s+profile:.*404`)},
		},
	})
	srv.AssertWalks(routeGetProfile, "beta=true")
	srv.assertRequestShape(t)
}

// TestUserProfilesDataSource_pages lists every profile over two pages,
// newest first by default.
func TestUserProfilesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 2)
	srv.threeProfiles()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_user_profiles" "test" {}`,
			ConfigStateChecks: dataChecks("data.anthropic_user_profiles.test", map[string]knownvalue.Check{
				"order": knownvalue.Null(),
				"user_profiles": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(profileObject(3)), knownvalue.ObjectExact(profileObject(2)), knownvalue.ObjectExact(profileObject(1)),
				}),
			}),
		}},
	})
	srv.AssertWalks(routeListProfiles, "beta=true", "beta=true&page="+sdktest.NextPageToken(profileID(2)))
	srv.AssertAllPagesRead(routeListProfiles)
	srv.assertRequestShape(t)
}

// TestUserProfilesDataSource_orderAsc sends order=asc and walks every page.
func TestUserProfilesDataSource_orderAsc(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 2)
	srv.threeProfiles()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_user_profiles" "test" { order = "asc" }`,
			ConfigStateChecks: dataChecks("data.anthropic_user_profiles.test", map[string]knownvalue.Check{
				"user_profiles": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(profileObject(1)), knownvalue.ObjectExact(profileObject(2)), knownvalue.ObjectExact(profileObject(3)),
				}),
			}),
		}},
	})
	srv.AssertWalks(routeListProfiles, "beta=true&order=asc", "beta=true&order=asc&page="+sdktest.NextPageToken(profileID(2)))
	srv.AssertAllPagesRead(routeListProfiles)
}

// TestUserProfilesDataSource_emptyAndError: no profile is an empty list,
// never null; a failed list (the 404 of an organization without the beta)
// is an error.
func TestUserProfilesDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 0)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_user_profiles" "test" {}`,
				ConfigStateChecks: dataChecks("data.anthropic_user_profiles.test", map[string]knownvalue.Check{
					"user_profiles": knownvalue.ListExact([]knownvalue.Check{}),
				}),
			},
			{
				PreConfig:   func() { srv.FailNext(routeListProfiles, http.StatusNotFound, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_user_profiles" "other" {}`,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+user\s+profiles:.*404`),
			},
		},
	})
}

// TestUserProfiles_requireAPIKey: the resource and both data sources refuse
// to run without the standard key, before any request.
func TestUserProfiles_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_user_profile resource": `resource "anthropic_user_profile" "test" { access_type = "application" }`,
		"anthropic_user_profile":          `data "anthropic_user_profile" "test" { id = "uprof_x" }`,
		"anthropic_user_profiles":         `data "anthropic_user_profiles" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newProfilesFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey, sdktest.AuthToken) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestUserProfiles_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestUserProfiles_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	userprofiles.NewUserProfileResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_user_profile: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_user_profile":  userprofiles.NewUserProfileDataSource(),
		"anthropic_user_profiles": userprofiles.NewUserProfilesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
