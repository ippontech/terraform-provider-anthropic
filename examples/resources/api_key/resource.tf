terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# API keys cannot be created via Terraform — import an existing key first:
#   terraform import anthropic_api_key.example <api_key_id>
resource "anthropic_api_key" "example" {
  name   = "My Managed Key"
  status = "active"
}

output "api_key_id" {
  value = anthropic_api_key.example.id
}

output "api_key_partial_hint" {
  value = anthropic_api_key.example.partial_key_hint
}
