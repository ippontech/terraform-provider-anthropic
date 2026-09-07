# tests/ is the root module `terraform test` runs in (`terraform -chdir=tests test`,
# see `make terraform-test`),
# so the provider repository root stays free of Terraform configuration.
#
# The provider and mock_provider blocks declared in the *.tftest.hcl files are
# resolved against THIS root module, not against the example modules the run
# blocks source (../examples/...). Without this mapping the local name
# "anthropic" falls back to hashicorp/anthropic and every test-file provider
# block fails with "unknown provider registry.terraform.io/hashicorp/anthropic"
# (#233).
terraform {
  # terraform test (1.6) and mock_provider (1.7) are the features this root exists for.
  required_version = "~> 1.7"

  required_providers {
    anthropic = {
      source = "registry.terraform.io/ippontech/anthropic"
    }
  }
}
