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
  api_key       = "dummy-api-key"
  admin_api_key = "dummy-admin-api-key"
}

resource "anthropic_workspace" "example" {
  name = "plan-test-workspace"

  data_residency = {
    workspace_geo          = "us"
    default_inference_geo  = "global"
    allowed_inference_geos = ["unrestricted"]
  }
}

output "workspace_name" {
  value = anthropic_workspace.example.name
}

output "workspace_geo" {
  value = anthropic_workspace.example.data_residency.workspace_geo
}

output "default_inference_geo" {
  value = anthropic_workspace.example.data_residency.default_inference_geo
}

output "allowed_inference_geos" {
  value = anthropic_workspace.example.data_residency.allowed_inference_geos
}
