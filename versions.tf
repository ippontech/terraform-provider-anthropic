# Root-module provider requirements for `terraform test`.
#
# The provider and mock_provider blocks declared in tests/*.tftest.hcl are
# resolved against THIS root module, not against the example modules the run
# blocks source. Without this mapping the local name "anthropic" falls back to
# hashicorp/anthropic and every test-file provider block fails with
# "unknown provider registry.terraform.io/hashicorp/anthropic" (#233).
terraform {
  # terraform test (1.6) and mock_provider (1.7) are the features this root exists for.
  required_version = "~> 1.7"

  required_providers {
    anthropic = {
      source = "registry.terraform.io/ippontech/anthropic"
    }
  }
}
