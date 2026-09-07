data "anthropic_federation_rule" "example" {
  id = "fdrl_01ABCDEFabcdef0123456789XY"
}

output "federation_rule_id" {
  description = "ID of the federation rule."
  value       = data.anthropic_federation_rule.example.id
}

output "federation_rule_target_service_account_id" {
  description = "ID of the service account that tokens minted through the rule act as."
  value       = data.anthropic_federation_rule.example.target.service_account_id
}
