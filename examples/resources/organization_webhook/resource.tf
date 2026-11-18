resource "anthropic_organization_webhook" "example" {
  url  = "https://example.com/anthropic-webhooks"
  name = "Enforcement actions"

  enabled_events = [
    "user_profile.enforcement_action_opened",
    "user_profile.enforcement_action_resolved",
  ]

  # Change this value to regenerate the signing secret. The old secret stops
  # working at once, so update the receiver in the same apply.
  signing_secret_version = 1
}

output "organization_webhook_id" {
  description = "ID of the organization webhook endpoint."
  value       = anthropic_organization_webhook.example.id
}

output "organization_webhook_signing_secret" {
  description = "Secret to verify the Standard Webhooks signature of each delivery with."
  value       = anthropic_organization_webhook.example.signing_secret
  sensitive   = true
}
