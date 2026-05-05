// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccCheckEnvironmentDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_environment" {
			continue
		}
		_, err := client.Beta.Environments.Get(context.Background(), rs.Primary.ID, anthropic.BetaEnvironmentGetParams{})
		if err != nil {
			// Resource not found — destroyed successfully.
			return nil
		}
		return fmt.Errorf("environment %s still exists", rs.Primary.ID)
	}
	return nil
}

const testAccEnvironmentResourceBasicConfig = `
resource "anthropic_environment" "test" {
  name = "tf-acc-test-env-basic"
}
`

const testAccEnvironmentResourceWithDescriptionConfig = `
resource "anthropic_environment" "test" {
  name        = "tf-acc-test-env-description"
  description = "Test environment with description"

  metadata = {
    team = "terraform"
    env  = "test"
  }
}
`

const testAccEnvironmentResourceWithLimitedNetworkConfig = `
resource "anthropic_environment" "test" {
  name = "tf-acc-test-env-limited"

  config = {
    networking = {
      type                   = "limited"
      allow_package_managers = true
      allowed_hosts          = ["example.com", "api.example.com"]
    }
    packages = {
      pip = ["requests", "boto3"]
    }
  }
}
`

const testAccEnvironmentResourceUpdateConfigV1 = `
resource "anthropic_environment" "test" {
  name = "tf-acc-test-env-update-v1"
}
`

const testAccEnvironmentResourceUpdateConfigV2 = `
resource "anthropic_environment" "test" {
  name        = "tf-acc-test-env-update-v2"
  description = "Updated description"
}
`

func TestAccEnvironmentResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccEnvironmentResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-basic"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "type", "environment"),
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "updated_at"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_environment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccEnvironmentResource_withDescription(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentResourceWithDescriptionConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-description"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "description", "Test environment with description"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "metadata.team", "terraform"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "metadata.env", "test"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_environment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccEnvironmentResource_withLimitedNetwork(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentResourceWithLimitedNetworkConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-limited"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "config.networking.type", "limited"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "config.networking.allow_package_managers", "true"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "config.networking.allowed_hosts.0", "example.com"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "config.packages.pip.0", "requests"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_environment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccEnvironmentResource_update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			// Create
			{
				Config: testAccEnvironmentResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-update-v1"),
				),
			},
			// Update name and description
			{
				Config: testAccEnvironmentResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-update-v2"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "description", "Updated description"),
				),
			},
		},
	})
}
