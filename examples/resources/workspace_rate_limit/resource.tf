resource "anthropic_workspace" "example" {
  name = "Rate-limited Workspace"
}

# Cap the workspace's throughput on one model group. Only the limiter types
# listed here are managed; the others keep inheriting the organization limits.
resource "anthropic_workspace_rate_limit" "sonnet" {
  workspace_id  = anthropic_workspace.example.id
  rate_limit_id = var.sonnet_rate_limit_id

  limits = {
    requests_per_minute      = 100
    output_tokens_per_minute = 20000
  }
}

variable "sonnet_rate_limit_id" {
  type        = string
  description = "ID of the organization's rate-limit entry for the model group, e.g. a rate_limit_id from the anthropic_workspace_rate_limits data source with include_inherited = true."
}

output "rate_limit_group" {
  description = "The rate-limit group the overrides apply to."
  value       = anthropic_workspace_rate_limit.sonnet.group
}
