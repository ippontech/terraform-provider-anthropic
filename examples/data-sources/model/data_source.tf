terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

data "anthropic_model" "example" {
  model_id = "claude-sonnet-4-5"
}

output "model" {
  description = "Information about the retrieved Anthropic model."
  value       = data.anthropic_model.example
}
