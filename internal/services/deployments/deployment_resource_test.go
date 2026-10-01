// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Deployments are a standard-API (non-admin) resource scoped to the workspace
// of the ANTHROPIC_API_KEY used by the test, so they support full create/read/
// update/delete acceptance tests (unlike the admin-API resources blocked by
// #58). Like vaults, deployments are free to manage. Unlike vaults, there is
// no hard-delete endpoint: destroying a deployment always archives it, so
// (again like anthropic_service_account) every acceptance run leaves an
// archived deployment behind in the test workspace — there is no cleanup step
// that could remove it.

func newAccTestClient() anthropic.Client {
	return *acctest.NewAPIKeyClient()
}

// testAccCheckDeploymentArchived verifies every anthropic_deployment in state
// was archived (not left active) by destroy.
func testAccCheckDeploymentArchived(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_deployment" {
			continue
		}
		if err := awaitDeploymentArchivedForTest(client, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// awaitDeploymentArchivedForTest polls Get until archived_at is set, riding
// out the same read-after-write staleness window documented for the vaults
// API (see CLAUDE.md); deployments share the underlying Beta managed-agents
// infrastructure.
func awaitDeploymentArchivedForTest(client anthropic.Client, id string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		deployment, err := client.Beta.Deployments.Get(context.Background(), id, anthropic.BetaDeploymentGetParams{})
		if err != nil {
			var apierr *anthropic.Error
			if errors.As(err, &apierr) && apierr.StatusCode == 404 {
				return fmt.Errorf("deployment %s not found while waiting for it to be archived", id)
			}
			return err
		}
		if !deployment.ArchivedAt.IsZero() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deployment %s was not archived within the timeout", id)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

const testAccDeploymentResourceBasicConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-deployment-agent"
}

resource "anthropic_environment" "test" {
  name = "tf-acc-test-deployment-environment"
}

resource "anthropic_deployment" "test" {
  name           = "tf-acc-test-deployment-basic"
  agent_id       = anthropic_agent.test.id
  environment_id = anthropic_environment.test.id

  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        { type = "text", text = "hello" }
      ]
    }
  ])
}
`

const testAccDeploymentResourceUpdatedConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-deployment-agent"
}

resource "anthropic_environment" "test" {
  name = "tf-acc-test-deployment-environment"
}

resource "anthropic_deployment" "test" {
  name           = "tf-acc-test-deployment-updated"
  description    = "updated description"
  agent_id       = anthropic_agent.test.id
  environment_id = anthropic_environment.test.id

  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        { type = "text", text = "hello" }
      ]
    }
  ])

  schedule = {
    expression = "0 9 * * 1-5"
    timezone   = "UTC"
  }
}
`

const testAccDeploymentResourcePausedConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-deployment-agent"
}

resource "anthropic_environment" "test" {
  name = "tf-acc-test-deployment-environment"
}

resource "anthropic_deployment" "test" {
  name           = "tf-acc-test-deployment-updated"
  description    = "updated description"
  agent_id       = anthropic_agent.test.id
  environment_id = anthropic_environment.test.id

  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        { type = "text", text = "hello" }
      ]
    }
  ])

  schedule = {
    expression = "0 9 * * 1-5"
    timezone   = "UTC"
  }

  paused = true
}
`

const testAccDeploymentResourceBudgetAndEmptyCollectionsConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-deployment-agent-budget"
}

resource "anthropic_environment" "test" {
  name = "tf-acc-test-deployment-environment-budget"
}

resource "anthropic_deployment" "test" {
  name           = "tf-acc-test-deployment-budget"
  agent_id       = anthropic_agent.test.id
  environment_id = anthropic_environment.test.id

  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        { type = "text", text = "hello" }
      ]
    }
  ])

  budget = jsonencode({
    max_list_cost = { amount = "2500", currency = "USD" }
    type          = "limit"
  })

  metadata  = {}
  vault_ids = []
}
`

// TestAccDeploymentResource_budgetAndEmptyCollectionsEmptyPlan guards two
// review findings: (1) that jsontypes.Normalized JSON round-tripped through
// the SDK's typed budget param and back via RawJSON() doesn't drift from the
// planned config (e.g. from server-populated fields or key reordering beyond
// what jsontypes.Normalized tolerates), and (2) that an empty (not null)
// `metadata`/`vault_ids` in config stays empty rather than collapsing to null
// once mapped from the API's own empty response, which would otherwise fail
// with "Provider produced inconsistent result after apply". The second apply
// step repeats the same config and asserts an empty plan.
func TestAccDeploymentResource_budgetAndEmptyCollectionsEmptyPlan(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckDeploymentArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccDeploymentResourceBudgetAndEmptyCollectionsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_deployment.test", "budget"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "metadata.%", "0"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "vault_ids.#", "0"),
				),
			},
			{
				Config:   testAccDeploymentResourceBudgetAndEmptyCollectionsConfig,
				PlanOnly: true,
			},
		},
	})
}

func TestAccDeploymentResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckDeploymentArchived,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccDeploymentResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_deployment.test", "id"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "name", "tf-acc-test-deployment-basic"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "status", "active"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "paused", "false"),
					resource.TestCheckResourceAttrSet("anthropic_deployment.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_deployment.test", "updated_at"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_deployment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Update: name, description, schedule
			{
				Config: testAccDeploymentResourceUpdatedConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_deployment.test", "name", "tf-acc-test-deployment-updated"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "description", "updated description"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "schedule.expression", "0 9 * * 1-5"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "schedule.timezone", "UTC"),
				),
			},
			// Update: pause via the dedicated pause endpoint
			{
				Config: testAccDeploymentResourcePausedConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_deployment.test", "paused", "true"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "status", "paused"),
				),
			},
			// Unpause
			{
				Config: testAccDeploymentResourceUpdatedConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_deployment.test", "paused", "false"),
					resource.TestCheckResourceAttr("anthropic_deployment.test", "status", "active"),
				),
			},
		},
	})
}
