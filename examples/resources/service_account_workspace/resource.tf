# Identity the explicit membership below is granted to. Federated tokens
# minted for this service account can only act in a workspace once it is
# an explicit member of that workspace (or the org default workspace, where
# every service account already has an implicit membership).
resource "anthropic_service_account" "ci" {
  name = "ci-deploy"
}

resource "anthropic_service_account_workspace" "ci" {
  service_account_id = anthropic_service_account.ci.id
  workspace_id       = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
  workspace_role     = "workspace_developer"
}

output "service_account_workspace_id" {
  description = "Composite ID of the membership (`<service_account_id>:<workspace_id>`)."
  value       = anthropic_service_account_workspace.ci.id
}

output "service_account_workspace_implicit" {
  description = "Whether the membership is implicit (org default workspace) rather than explicit."
  value       = anthropic_service_account_workspace.ci.implicit
}
