# Requires an org:admin OAuth bearer token (auth_token / ANTHROPIC_AUTH_TOKEN);
# Admin API keys are not accepted on this endpoint.
data "anthropic_service_accounts" "example" {}

output "service_accounts" {
  description = "List of Workload Identity Federation service accounts in the organization."
  value       = data.anthropic_service_accounts.example.service_accounts
}
