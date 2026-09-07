data "anthropic_federation_rule" "example" {
  id = "fdrl_01ABCDEFabcdef0123456789XY"
}

output "federation_rule_id" {
  value = data.anthropic_federation_rule.example.id
}

output "federation_rule_target_service_account_id" {
  value = data.anthropic_federation_rule.example.target.service_account_id
}
