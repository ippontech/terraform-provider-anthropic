# Minimal service account (organization_role defaults to "developer")
resource "anthropic_service_account" "ci_runner" {
  name        = "ci-runner"
  description = "Workload identity used by the CI pipeline"
}

# Admin-role service account. Federation rules may only grant org:admin scope
# to a service account whose organization_role is "admin", and setting this
# role requires an interactive credential (a user OAuth token or a Console
# session) - a workload alone cannot create or promote one.
resource "anthropic_service_account" "release_manager" {
  name              = "release-manager"
  organization_role = "admin"
}

output "service_account_id" {
  description = "ID of the CI runner service account."
  value       = anthropic_service_account.ci_runner.id
}

output "service_account_organization_role" {
  description = "Organization role of the CI runner service account."
  value       = anthropic_service_account.ci_runner.organization_role
}

output "service_account_name" {
  description = "Name of the CI runner service account."
  value       = anthropic_service_account.ci_runner.name
}

output "release_manager_role" {
  description = "Organization role of the release manager service account."
  value       = anthropic_service_account.release_manager.organization_role
}
