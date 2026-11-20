data "anthropic_models" "example" {}

# Retired models are listed only when requested.
data "anthropic_models" "deprecated" {
  lifecycle_stages = ["deprecated"]
}

output "models" {
  description = "List of available Anthropic models."
  value       = data.anthropic_models.example.models
}

output "deprecated_models" {
  description = "Deprecated models and their scheduled retirement dates (null when none is scheduled yet)."
  value       = { for m in data.anthropic_models.deprecated.models : m.id => m.retires_at }
}
