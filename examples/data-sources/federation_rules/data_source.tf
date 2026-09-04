terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# List all federation rules in the organization.
data "anthropic_federation_rules" "all" {}

output "federation_rules_count" {
  value = length(data.anthropic_federation_rules.all.rules)
}

output "federation_rule_names" {
  value = [for r in data.anthropic_federation_rules.all.rules : r.name]
}

# Filter to the rules referencing a specific issuer, and include archived ones.
data "anthropic_federation_rules" "by_issuer" {
  issuer_id        = "fdis_01exampleIssuerID"
  include_archived = true
}

output "rules_for_issuer" {
  value = [for r in data.anthropic_federation_rules.by_issuer.rules : r.id]
}
