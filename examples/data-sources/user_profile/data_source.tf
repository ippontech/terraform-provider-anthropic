data "anthropic_user_profile" "example" {
  id = "uprof_01AAAAAAAAAAAAAAAAAAAAAA"
}

output "user_profile_id" {
  description = "ID of the user profile."
  value       = data.anthropic_user_profile.example.id
}

output "user_profile_access_type" {
  description = "Access type of the user profile (`application` or `passthrough`)."
  value       = data.anthropic_user_profile.example.access_type
}

output "user_profile_name" {
  description = "Real-world name of the entity the user profile represents."
  value       = data.anthropic_user_profile.example.name
  sensitive   = true
}
