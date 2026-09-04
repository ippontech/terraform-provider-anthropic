terraform {
  required_version = ">= 1.6"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Resolve a real federation issuer ID from the list data source: a constant ID
# would 404 against a live organization, and this also keeps the ID
# unknown-at-plan so the read genuinely happens at apply time.
data "anthropic_federation_issuers" "all" {}

data "anthropic_federation_issuer" "example" {
  id = data.anthropic_federation_issuers.all.issuers[0].id
}

output "federation_issuer_name" {
  value = data.anthropic_federation_issuer.example.name
}

output "federation_issuer_issuer_url" {
  value = data.anthropic_federation_issuer.example.issuer_url
}
