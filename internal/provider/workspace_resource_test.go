// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccAdminAPIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("ANTHROPIC_ADMIN_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_ADMIN_API_KEY must be set for workspace acceptance tests")
	}
	return key
}

func testAccCheckWorkspaceArchived(adminKey, workspaceID string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := newAdminClient(adminKey)
		respBytes, err := client.doRequest(context.Background(), "GET", "/v1/organizations/workspaces/"+workspaceID, nil)
		if err != nil {
			return fmt.Errorf("read workspace %s: %w", workspaceID, err)
		}
		var ws workspaceAPIResponse
		if err := json.Unmarshal(respBytes, &ws); err != nil {
			return fmt.Errorf("parse workspace response: %w", err)
		}
		if ws.ArchivedAt == nil || *ws.ArchivedAt == "" {
			return fmt.Errorf("workspace %s is not archived after destroy", workspaceID)
		}
		return nil
	}
}

func TestAccWorkspaceResource_basic(t *testing.T) {
	adminKey := testAccAdminAPIKey(t)
	var workspaceID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			if workspaceID == "" {
				return nil
			}
			return testAccCheckWorkspaceArchived(adminKey, workspaceID)(s)
		},
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccWorkspaceResourceBasicConfig(adminKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "id"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "name", "tf-acc-test-workspace"),
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "display_color"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "type", "workspace"),
					resource.TestCheckNoResourceAttr("anthropic_workspace.test", "archived_at"),
					// Capture the ID for the destroy checker.
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["anthropic_workspace.test"]
						if !ok {
							return fmt.Errorf("anthropic_workspace.test not found in state")
						}
						workspaceID = rs.Primary.ID
						return nil
					},
				),
			},
			// ImportState round-trip
			{
				ResourceName:      "anthropic_workspace.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccWorkspaceResource_withDataResidency(t *testing.T) {
	adminKey := testAccAdminAPIKey(t)
	var workspaceID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			if workspaceID == "" {
				return nil
			}
			return testAccCheckWorkspaceArchived(adminKey, workspaceID)(s)
		},
		Steps: []resource.TestStep{
			// Create with explicit data_residency
			{
				Config: testAccWorkspaceResourceWithDataResidencyConfig(adminKey, "tf-acc-test-ws-dr"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "id"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "data_residency.workspace_geo", "us"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "data_residency.default_inference_geo", "global"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "data_residency.allowed_inference_geos.0", "unrestricted"),
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["anthropic_workspace.test"]
						if !ok {
							return fmt.Errorf("anthropic_workspace.test not found in state")
						}
						workspaceID = rs.Primary.ID
						return nil
					},
				),
			},
			// In-place update: rename the workspace; workspace_geo must stay the same (no replacement).
			{
				Config: testAccWorkspaceResourceWithDataResidencyConfig(adminKey, "tf-acc-test-ws-dr-updated"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace.test", "name", "tf-acc-test-ws-dr-updated"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "data_residency.workspace_geo", "us"),
				),
			},
		},
	})
}

func testAccWorkspaceResourceBasicConfig(adminKey string) string {
	return fmt.Sprintf(`
provider "anthropic" {
  admin_api_key = %q
}

resource "anthropic_workspace" "test" {
  name = "tf-acc-test-workspace"
}
`, adminKey)
}

func testAccWorkspaceResourceWithDataResidencyConfig(adminKey, name string) string {
	return fmt.Sprintf(`
provider "anthropic" {
  admin_api_key = %q
}

resource "anthropic_workspace" "test" {
  name = %q

  data_residency = {
    workspace_geo          = "us"
    default_inference_geo  = "global"
    allowed_inference_geos = ["unrestricted"]
  }
}
`, adminKey, name)
}
