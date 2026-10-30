// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

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

const modelsConfig = `
data "anthropic_models" "test" {}
`

func modelCheck(id, displayName, createdAt string, maxInput, maxTokens int64, bits uint) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":               knownvalue.StringExact(id),
		"display_name":     knownvalue.StringExact(displayName),
		"created_at":       knownvalue.StringExact(createdAt),
		"max_input_tokens": knownvalue.Int64Exact(maxInput),
		"max_tokens":       knownvalue.Int64Exact(maxTokens),
		"capabilities":     capabilitiesCheck(bits),
	})
}

// TestModelsDataSource_pages lists five models served two per page: the data
// source follows last_id with after_id until has_more is false, keeps the
// API's order and maps every model, and asks for the largest page.
func TestModelsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 2,
		modelJSON("m1", "Model 1", "2026-05-01T00:00:00Z", 1001, 101, bitsA),
		modelJSON("m2", "Model 2", "2026-04-01T00:00:00Z", 1002, 102, bitsB),
		modelJSON("m3", "Model 3", "2026-03-01T00:00:00Z", 1003, 103, bitsA),
		modelJSON("m4", "Model 4", "2026-02-01T00:00:00Z", 1004, 104, bitsB),
		modelJSON("m5", "Model 5", "2026-01-01T00:00:00.5Z", 1005, 105, bitsA),
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + modelsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_models.test", tfjsonpath.New("models"), knownvalue.ListExact([]knownvalue.Check{
					modelCheck("m1", "Model 1", "2026-05-01T00:00:00Z", 1001, 101, bitsA),
					modelCheck("m2", "Model 2", "2026-04-01T00:00:00Z", 1002, 102, bitsB),
					modelCheck("m3", "Model 3", "2026-03-01T00:00:00Z", 1003, 103, bitsA),
					modelCheck("m4", "Model 4", "2026-02-01T00:00:00Z", 1004, 104, bitsB),
					modelCheck("m5", "Model 5", "2026-01-01T00:00:00.5Z", 1005, 105, bitsA),
				})),
			},
		}},
	})

	srv.AssertWalks(routeListModels, "limit=1000", "after_id=m2&limit=1000", "after_id=m4&limit=1000")
	srv.AssertAllPagesRead(routeListModels)
	srv.AssertCredential(routeListModels, sdktest.APIKey)
	srv.AssertNoBeta(routeListModels)
}

// TestModelsDataSource_empty: no model is an empty list, never null.
func TestModelsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + modelsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_models.test", tfjsonpath.New("models"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListModels, "limit=1000")
}

// TestModelsDataSource_error: an API error is an error diagnostic with the
// API's message, not a short list.
func TestModelsDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0, modelJSON("m1", "Model 1", "2026-05-01T00:00:00Z", 1, 1, bitsA))
	srv.FailNext(routeListModels, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.APIKey) + modelsConfig,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+models:.*403\s+Forbidden.*injected\s+403`),
		}},
	})
	// A 403 is not retried: one request, then the error.
	srv.AssertCalled(routeListModels, 1)
}

// TestModelsDataSource_requiresAPIKey: with only an OAuth token the data
// source refuses to run.
func TestModelsDataSource_requiresAPIKey(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AuthToken) + modelsConfig,
			ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
		}},
	})
	srv.AssertCalled("", 0)
}
