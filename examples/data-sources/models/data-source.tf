terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

data "anthropic_models" "example" {}

output "models" {
  description = "List of available Anthropic models."
  value       = data.anthropic_models.example.models
}
