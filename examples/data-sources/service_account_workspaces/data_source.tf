data "anthropic_service_account_workspaces" "example" {
  service_account_id = "svac_01WCz1FkmYMm4gnmykNKUu3Q"
}

output "service_account_workspaces_count" {
  description = "Total number of workspace memberships (implicit and explicit) for the service account."
  value       = length(data.anthropic_service_account_workspaces.example.workspaces)
}
