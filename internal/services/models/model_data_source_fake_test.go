// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/models"
)

// TestModelDataSource_read reads a model by alias through Terraform: the
// request goes to /v1/models/{alias} with the standard key and no beta, and
// every attribute comes from the response, id included (not from model_id).
func TestModelDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0,
		modelJSON("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "2025-10-01T00:00:00Z", 200000, 64000, bitsB),
		modelJSON("claude-opus-5-5-20260921", "Claude Opus 5.5", "2026-09-21T10:30:00.25Z", 1000000, 128000, bitsA),
	)
	srv.aliases["claude-opus-5-5"] = "claude-opus-5-5-20260921"

	const addr = "data.anthropic_model.test"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_model" "test" {
  model_id = "claude-opus-5-5"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("model_id"), knownvalue.StringExact("claude-opus-5-5")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact("claude-opus-5-5-20260921")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("display_name"), knownvalue.StringExact("Claude Opus 5.5")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-09-21T10:30:00.25Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("max_input_tokens"), knownvalue.Int64Exact(1000000)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("max_tokens"), knownvalue.Int64Exact(128000)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("capabilities"), capabilitiesCheck(bitsA)),
			},
		}},
	})

	srv.AssertWalks(routeGetModel, "")
	for _, r := range srv.Requests(routeGetModel) {
		if r.Path != "/v1/models/claude-opus-5-5" {
			t.Errorf("GET %s, want /v1/models/claude-opus-5-5", r.Path)
		}
	}
	srv.AssertCredential(routeGetModel, sdktest.APIKey)
	srv.AssertNoBeta(routeGetModel)
	srv.AssertCalled(routeListModels, 0)
}

// TestModelDataSource_otherModel guards against a mapping that ignores the
// response: the same configuration shape on another model yields that
// model's values.
func TestModelDataSource_otherModel(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0,
		modelJSON("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "2025-10-01T00:00:00Z", 200000, 64000, bitsB),
	)

	const addr = "data.anthropic_model.test"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_model" "test" {
  model_id = "claude-haiku-4-5-20251001"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact("claude-haiku-4-5-20251001")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("display_name"), knownvalue.StringExact("Claude Haiku 4.5")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact("2025-10-01T00:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("max_input_tokens"), knownvalue.Int64Exact(200000)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("max_tokens"), knownvalue.Int64Exact(64000)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("capabilities"), capabilitiesCheck(bitsB)),
			},
		}},
	})
}

// TestModelDataSource_notFound: an unknown model is an error diagnostic
// that carries the API's message, never an empty data source.
func TestModelDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_model" "test" {
  model_id = "claude-unknown"
}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+model:.*404\s+Not\s+Found.*model:\s+claude-unknown`),
		}},
	})
	srv.AssertWalks(routeGetModel, "")
}

// TestModelDataSource_requiresAPIKey: with only an Admin key the data source
// refuses to run instead of calling the API without a credential.
func TestModelDataSource_requiresAPIKey(t *testing.T) {
	t.Parallel()
	srv := newModelsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_model" "test" {
  model_id = "claude-opus-5-5"
}
`,
			ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
		}},
	})
	srv.AssertCalled("", 0)
}

// TestModelDataSources_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestModelDataSources_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	for name, d := range map[string]datasource.DataSource{
		"anthropic_model":  models.NewModelDataSource(),
		"anthropic_models": models.NewModelsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not provider data"}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(string) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
