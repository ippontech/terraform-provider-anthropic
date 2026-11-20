# Read the organization tied to the configured Admin API credential
data "anthropic_organization" "current" {}

output "organization_id" {
  description = "ID of the organization."
  value       = data.anthropic_organization.current.id
}

output "organization_name" {
  description = "Name of the organization."
  value       = data.anthropic_organization.current.name
}

output "organization_type" {
  description = "Object type returned by the API (`organization`)."
  value       = data.anthropic_organization.current.type
}
