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
  admin_api_key = "dummy-admin-api-key"
}

resource "anthropic_api_key" "example" {
  name   = "plan-test-key"
  status = "active"
}

output "api_key_name" {
  value = anthropic_api_key.example.name
}

output "api_key_status" {
  value = anthropic_api_key.example.status
}
