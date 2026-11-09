# Turn on the Compliance API and its activity recording for the organization of
# the admin key. Destroying this resource leaves the setting as it is.
resource "anthropic_compliance_settings" "this" {
  state = "enabled"
}

output "compliance_api_state" {
  description = "Whether the Compliance API is enabled for the organization."
  value       = anthropic_compliance_settings.this.state
}
