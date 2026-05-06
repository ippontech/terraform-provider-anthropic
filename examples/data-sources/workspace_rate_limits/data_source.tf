terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

data "anthropic_workspace_rate_limits" "example" {
  workspace_id = var.workspace_id
}

variable "workspace_id" {
  type        = string
  description = "The ID of the workspace to list rate-limit overrides for."
}

output "rate_limits_count" {
  value = length(data.anthropic_workspace_rate_limits.example.rate_limits)
}
