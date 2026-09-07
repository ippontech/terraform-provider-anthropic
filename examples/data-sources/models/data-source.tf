data "anthropic_models" "example" {}

output "models" {
  description = "List of available Anthropic models."
  value       = data.anthropic_models.example.models
}
