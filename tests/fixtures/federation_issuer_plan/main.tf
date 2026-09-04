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
  auth_token = "dummy-auth-token"
}

resource "anthropic_federation_issuer" "example" {
  name       = "github-actions"
  issuer_url = "https://token.actions.githubusercontent.com"

  jwks = {
    type = "discovery"
  }

  max_jwt_lifetime_seconds = 600
}

output "federation_issuer_name" {
  value = anthropic_federation_issuer.example.name
}

output "federation_issuer_issuer_url" {
  value = anthropic_federation_issuer.example.issuer_url
}

output "federation_issuer_jwks_type" {
  value = anthropic_federation_issuer.example.jwks.type
}

output "federation_issuer_max_jwt_lifetime_seconds" {
  value = anthropic_federation_issuer.example.max_jwt_lifetime_seconds
}
