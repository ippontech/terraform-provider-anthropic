# All runs across every deployment in the workspace.
data "anthropic_deployment_runs" "all" {}

# Only the failed scheduled runs of one deployment (`depl_` + 24 characters).
data "anthropic_deployment_runs" "failed" {
  deployment_id = "depl_01AAAAAAAAAAAAAAAAAAAAAA"
  has_error     = true
  trigger_type  = "schedule"
}

output "run_count" {
  description = "Number of deployment runs in the workspace."
  value       = length(data.anthropic_deployment_runs.all.runs)
}

output "failed_run_errors" {
  description = "Error types of the failed scheduled runs of the deployment."
  value       = [for r in data.anthropic_deployment_runs.failed.runs : r.error.type]
}
