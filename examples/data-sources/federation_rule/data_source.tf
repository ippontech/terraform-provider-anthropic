# Resolve a real federation rule ID from the list data source, then fetch its
# full details by ID.
data "anthropic_federation_rules" "all" {}

data "anthropic_federation_rule" "example" {
  id = data.anthropic_federation_rules.all.rules[0].id
}

output "federation_rule_id" {
  value = data.anthropic_federation_rule.example.id
}

output "federation_rule_target_service_account_id" {
  value = data.anthropic_federation_rule.example.target.service_account_id
}
