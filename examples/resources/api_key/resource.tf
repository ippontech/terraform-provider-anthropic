# API keys cannot be created via Terraform — import an existing key first:
#   terraform import anthropic_api_key.example <api_key_id>
resource "anthropic_api_key" "example" {
  name   = "My Managed Key"
  status = "active"
}

output "api_key_id" {
  description = "ID of the managed API key."
  value       = anthropic_api_key.example.id
}

output "api_key_partial_hint" {
  description = "Last characters of the key, for identification without exposing the secret."
  value       = anthropic_api_key.example.partial_key_hint
}
