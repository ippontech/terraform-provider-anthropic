terraform {
  required_version = ">= 1.6"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {
  auth_token    = "dummy-auth-token"
  admin_api_key = "dummy-admin-key" # only for the seed resource's Configure guard
}

# Never applied: exists only so depends_on defers the data source read past
# plan. anthropic_service_account_workspaces has an all-known input (a literal
# placeholder service_account_id), so without this seed resource Terraform
# would read it live during `terraform plan` and fail against the dummy
# auth_token.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_service_account_workspaces" "test" {
  depends_on         = [anthropic_workspace.seed]
  service_account_id = "svac_01PLANTEST"
}

output "service_account_workspaces_service_account_id" {
  value = data.anthropic_service_account_workspaces.test.service_account_id
}
