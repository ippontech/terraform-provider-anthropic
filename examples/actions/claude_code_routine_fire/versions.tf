terraform {
  # Actions need Terraform 1.14.
  required_version = ">= 1.14"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}
