data "anthropic_service_account" "example" {
  id = "svac_01WCz1FkmYMm4gnmykNKUu3Q"
}

output "service_account_id" {
  value = data.anthropic_service_account.example.id
}

output "service_account_name" {
  value     = data.anthropic_service_account.example.name
  sensitive = true
}

output "service_account_organization_role" {
  value = data.anthropic_service_account.example.organization_role
}
