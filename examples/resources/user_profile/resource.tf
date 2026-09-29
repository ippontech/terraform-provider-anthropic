resource "anthropic_user_profile" "example" {
  access_type = "application"
  external_id = "customer-4821"
  name        = "Acme Corp"

  metadata = {
    tier = "gold"
  }
}

output "user_profile_id" {
  description = "ID of the created user profile."
  value       = anthropic_user_profile.example.id
}

output "user_profile_access_type" {
  description = "Access type of the created user profile."
  value       = anthropic_user_profile.example.access_type
}
