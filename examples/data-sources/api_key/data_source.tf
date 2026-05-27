terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Import an existing API key first, then look it up with the data source.
# The data source id references the resource id (unknown during plan),
# which defers the read until apply.
resource "anthropic_api_key" "managed" {
  name   = "example-key"
  status = "active"
}

data "anthropic_api_key" "example" {
  id = anthropic_api_key.managed.id
}

output "api_key_name" {
  value = data.anthropic_api_key.example.name
}

output "api_key_status" {
  value = data.anthropic_api_key.example.status
}

output "api_key_partial_hint" {
  value = data.anthropic_api_key.example.partial_key_hint
}
