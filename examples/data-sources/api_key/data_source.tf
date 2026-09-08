# Import an existing API key first, then look it up with the data source.
# The data source id references the resource id (unknown during plan),
# which defers the read until apply.
resource "anthropic_api_key" "managed" {
  name   = "example-key"
  status = "active"
}

data "anthropic_api_key" "example" {
  id = anthropic_api_key.managed.id
}

output "api_key_name" {
  description = "Display name of the API key."
  value       = data.anthropic_api_key.example.name
}

output "api_key_status" {
  description = "Lifecycle status of the API key."
  value       = data.anthropic_api_key.example.status
}

output "api_key_partial_hint" {
  description = "Last characters of the key, for identification without exposing the secret."
  value       = data.anthropic_api_key.example.partial_key_hint
}
