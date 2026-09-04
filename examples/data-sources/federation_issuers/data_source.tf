terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Requires an org:admin OAuth bearer token (auth_token / ANTHROPIC_AUTH_TOKEN);
# Admin API keys are not accepted by this endpoint.
data "anthropic_federation_issuers" "all" {}

output "federation_issuer_ids" {
  value = [for issuer in data.anthropic_federation_issuers.all.issuers : issuer.id]
}

output "federation_issuers_count" {
  value = length(data.anthropic_federation_issuers.all.issuers)
}

# Include archived issuers too.
data "anthropic_federation_issuers" "with_archived" {
  include_archived = true
}

output "federation_issuers_count_including_archived" {
  value = length(data.anthropic_federation_issuers.with_archived.issuers)
}
