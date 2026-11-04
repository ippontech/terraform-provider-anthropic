// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providertest

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func TestProviderConfig(t *testing.T) {
	for _, tc := range []struct {
		creds []sdktest.Credential
		want  string
	}{
		{nil, "provider \"anthropic\" {\n  api_key       = \"\"\n  admin_api_key = \"\"\n  auth_token    = \"\"\n}\n"},
		{[]sdktest.Credential{sdktest.APIKey}, "provider \"anthropic\" {\n  api_key       = \"sk-ant-api03-sdktest\"\n  admin_api_key = \"\"\n  auth_token    = \"\"\n}\n"},
		{[]sdktest.Credential{sdktest.AdminKey, sdktest.AuthToken}, "provider \"anthropic\" {\n  api_key       = \"\"\n  admin_api_key = \"sk-ant-admin01-sdktest\"\n  auth_token    = \"sk-ant-oat01-sdktest\"\n}\n"},
	} {
		if got := ProviderConfig(tc.creds...); got != tc.want {
			t.Errorf("ProviderConfig(%v) =\n%s\nwant\n%s", tc.creds, got, tc.want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("ProviderConfig accepted an unknown credential")
		}
	}()
	ProviderConfig(sdktest.Credential(0))
}

// TestProviderFactoriesReachTheFake runs a data source through Terraform:
// the request reaches the fake with the configured credential only, whatever
// ANTHROPIC_BASE_URL and the credential variables of the environment say.
func TestProviderFactoriesReachTheFake(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/models/{id}", http.StatusOK, map[string]any{
		"type": "model", "id": "m", "display_name": "M", "created_at": "2026-01-01T00:00:00Z",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: ProviderConfig(sdktest.APIKey) + `data "anthropic_model" "test" { model_id = "m" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_model.test", tfjsonpath.New("display_name"), knownvalue.StringExact("M")),
			},
		}},
	})
	srv.AssertCredential("GET /v1/models/{id}", sdktest.APIKey)
}
