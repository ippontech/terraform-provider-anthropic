# Plan-only fixture for tests/service_account.tftest.hcl.
#
# The public example (examples/resources/service_account) carries no provider
# block, so under `terraform test` the provider resolves credentials from the
# environment — and the terraform-test CI job has no ANTHROPIC_AUTH_TOKEN, so
# the resource's Configure failed with "Missing OAuth Token". Resources are
# never created under `command = plan`, so a dummy bearer token is enough to
# satisfy Configure while keeping the run fully offline. Same pattern as
# tests/fixtures/federation_issuer_plan.
terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {
  auth_token = "dummy-auth-token"
}

resource "anthropic_service_account" "ci_runner" {
  name        = "ci-runner"
  description = "Workload identity used by the CI pipeline"
}

resource "anthropic_service_account" "release_manager" {
  name              = "release-manager"
  organization_role = "admin"
}

output "service_account_name" {
  value = anthropic_service_account.ci_runner.name
}

output "release_manager_role" {
  value = anthropic_service_account.release_manager.organization_role
}
