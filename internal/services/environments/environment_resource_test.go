// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccCheckEnvironmentDestroyed(s *terraform.State) error {
	client := acctest.NewAPIKeyClient()
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

// testAccCheckEnvironmentArchivedAndCleanup verifies the environment was archived
// (not hard-deleted) and then permanently deletes it to avoid dangling resources.
func testAccCheckEnvironmentArchivedAndCleanup(s *terraform.State) error {
	client := acctest.NewAPIKeyClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_environment" {
			continue
		}
		env, err := client.Beta.Environments.Get(context.Background(), rs.Primary.ID, anthropic.BetaEnvironmentGetParams{})
		if err != nil {
			return fmt.Errorf("environment %s not found after archive destroy: %w", rs.Primary.ID, err)
		}
		if env.ArchivedAt == "" {
			return fmt.Errorf("environment %s was not archived on destroy", rs.Primary.ID)
		}
		// Hard-delete the archived environment so it doesn't accumulate in the org.
		if _, err := client.Beta.Environments.Delete(context.Background(), rs.Primary.ID, anthropic.BetaEnvironmentDeleteParams{}); err != nil {
			return fmt.Errorf("cleanup: unable to delete archived environment %s: %w", rs.Primary.ID, err)
		}
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

// Removing metadata keys and the description from the configuration clears
// them server-side, not only from state: the API applies metadata as a patch
// and keeps an omitted key, so the update sends a removed key as null, and it
// keeps an omitted description, so a removed one is sent as null
// (https://platform.claude.com/docs/en/api/beta/environments/update).
// `metadata = {}` stays an empty map in state although the API answers {}
// with or without metadata. The live counterpart of the fake-API
// TestEnvironmentResource_metadataKeyRemoved, plus the removal of the
// description and of the metadata attribute
// (TestEnvironmentResource_descriptionAndMetadataRemoved); the post-apply
// plan of every step must be empty (the default check).
func TestAccEnvironmentResource_metadataAndDescriptionRemoved(t *testing.T) {
	const description = "tf-acc-test description to remove"
	name := fmt.Sprintf("tf-acc-test-env-metadata-%d", time.Now().UnixNano())
	// In place, never a replacement: the out-of-band reads must all see the
	// environment the first step created.
	updateInPlace := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(environmentAddr, plancheck.ResourceActionUpdate)},
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentResourceMetadataConfig(name, description, `metadata = { team = "terraform", env = "test" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(environmentAddr, "description", description),
					resource.TestCheckResourceAttr(environmentAddr, "metadata.%", "2"),
					resource.TestCheckResourceAttr(environmentAddr, "metadata.team", "terraform"),
					resource.TestCheckResourceAttr(environmentAddr, "metadata.env", "test"),
					testAccCheckEnvironmentStored(description, map[string]string{"team": "terraform", "env": "test"}),
				),
			},
			// Remove one key: sent as null, deleted server-side.
			{
				Config:           testAccEnvironmentResourceMetadataConfig(name, description, `metadata = { team = "terraform" }`),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(environmentAddr, "metadata.%", "1"),
					resource.TestCheckResourceAttr(environmentAddr, "metadata.team", "terraform"),
					resource.TestCheckNoResourceAttr(environmentAddr, "metadata.env"),
					testAccCheckEnvironmentStored(description, map[string]string{"team": "terraform"}),
				),
			},
			// metadata = {}: the last key is deleted and the empty map stays in
			// state (the `metadata.%` check accepts a null map too, but
			// Terraform fails the step unless the state is the configured
			// empty map).
			{
				Config:           testAccEnvironmentResourceMetadataConfig(name, description, `metadata = {}`),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(environmentAddr, "metadata.%", "0"),
					resource.TestCheckNoResourceAttr(environmentAddr, "metadata.team"),
					testAccCheckEnvironmentStored(description, map[string]string{}),
				),
			},
			// Remove description and metadata: the description is sent as null
			// and cleared server-side, and metadata goes back to null in state.
			{
				Config:           testAccEnvironmentResourceMetadataConfig(name, "", ""),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(environmentAddr, "description"),
					resource.TestCheckNoResourceAttr(environmentAddr, "metadata.%"),
					testAccCheckEnvironmentStored("", map[string]string{}),
				),
			},
		},
	})
}

// testAccEnvironmentResourceMetadataConfig is an environment named name, with
// description ("" for none) and extra appended to its attributes.
func testAccEnvironmentResourceMetadataConfig(name, description, extra string) string {
	if description != "" {
		extra = fmt.Sprintf("description = %q\n  %s", description, extra)
	}
	return fmt.Sprintf(`
resource "anthropic_environment" "test" {
  name = %q
  %s
}
`, name, extra)
}

// testAccCheckEnvironmentStored reads the environment at environmentAddr
// (fake_test.go) through the API, outside the provider, and checks that its
// description ("" for none) and metadata are exactly the given ones: what the
// configuration removed must be gone server-side, not only from state.
func testAccCheckEnvironmentStored(description string, metadata map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[environmentAddr]
		if !ok {
			return fmt.Errorf("%s not found in state", environmentAddr)
		}
		env, err := acctest.NewAPIKeyClient().Beta.Environments.Get(context.Background(), rs.Primary.ID, anthropic.BetaEnvironmentGetParams{})
		if err != nil {
			return fmt.Errorf("reading environment %s: %w", rs.Primary.ID, err)
		}
		if env.Description != description || !maps.Equal(env.Metadata, metadata) {
			return fmt.Errorf("environment %s has description %q and metadata %v on the API, want %q and %v",
				rs.Primary.ID, env.Description, env.Metadata, description, metadata)
		}
		return nil
	}
}

const testAccEnvironmentResourceArchiveOnDestroyConfig = `
resource "anthropic_environment" "test" {
  name               = "tf-acc-test-env-archive-on-destroy"
  archive_on_destroy = true
}
`

func TestAccEnvironmentResource_archiveOnDestroy(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentArchivedAndCleanup,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentResourceArchiveOnDestroyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "name", "tf-acc-test-env-archive-on-destroy"),
					resource.TestCheckResourceAttr("anthropic_environment.test", "archive_on_destroy", "true"),
				),
			},
			// ImportState — archive_on_destroy is local-only; provider defaults it to false on import.
			{
				ResourceName:            "anthropic_environment.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}
