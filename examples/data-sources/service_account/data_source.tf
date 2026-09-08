data "anthropic_service_account" "example" {
  id = "svac_01WCz1FkmYMm4gnmykNKUu3Q"
}

output "service_account_id" {
  description = "ID of the service account."
  value       = data.anthropic_service_account.example.id
}

output "service_account_name" {
  description = "Name of the service account."
  value       = data.anthropic_service_account.example.name
  sensitive   = true
}

output "service_account_organization_role" {
  description = "Organization role of the service account (`developer` or `admin`)."
  value       = data.anthropic_service_account.example.organization_role
}
