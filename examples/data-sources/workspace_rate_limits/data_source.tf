data "anthropic_workspace_rate_limits" "example" {
  workspace_id = var.workspace_id
}

variable "workspace_id" {
  type        = string
  description = "The ID of the workspace to list rate-limit overrides for."
}

output "rate_limits_count" {
  description = "Number of rate-limit overrides configured for the workspace."
  value       = length(data.anthropic_workspace_rate_limits.example.rate_limits)
}
