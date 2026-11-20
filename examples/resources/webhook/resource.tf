resource "anthropic_webhook" "example" {
  url  = "https://example.com/anthropic-webhooks"
  name = "Session status"

  enabled_events = [
    "session.status_idled",
    "session.status_terminated",
  ]

  # Change this value to regenerate the signing secret. The old secret stops
  # working at once, so update the receiver in the same apply.
  signing_secret_version = 1
}

output "webhook_id" {
  description = "ID of the webhook endpoint."
  value       = anthropic_webhook.example.id
}

output "webhook_signing_secret" {
  description = "Secret to verify the Standard Webhooks signature of each delivery with."
  value       = anthropic_webhook.example.signing_secret
  sensitive   = true
}
