# Requires an org:admin OAuth bearer token (auth_token / ANTHROPIC_AUTH_TOKEN);
# Admin API keys are not accepted by this endpoint.
data "anthropic_federation_issuers" "all" {}

output "federation_issuer_ids" {
  description = "IDs of the live federation issuers."
  value       = [for issuer in data.anthropic_federation_issuers.all.issuers : issuer.id]
}

output "federation_issuers_count" {
  description = "Number of live federation issuers."
  value       = length(data.anthropic_federation_issuers.all.issuers)
}

# Include archived issuers too.
data "anthropic_federation_issuers" "with_archived" {
  include_archived = true
}

output "federation_issuers_count_including_archived" {
  description = "Number of federation issuers, archived ones included."
  value       = length(data.anthropic_federation_issuers.with_archived.issuers)
}
