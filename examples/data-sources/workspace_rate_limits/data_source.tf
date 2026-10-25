data "anthropic_workspace_rate_limits" "example" {
  workspace_id = var.workspace_id
}

# Every group the workspace can see, with the values it inherits from the
# organization next to its own overrides (each value's source.type says which).
data "anthropic_workspace_rate_limits" "effective" {
  workspace_id      = var.workspace_id
  include_inherited = true
}

variable "workspace_id" {
  type        = string
  description = "The ID of the workspace to list rate limits for."
}

output "rate_limits_count" {
  description = "Number of rate-limit groups with at least one override in the workspace."
  value       = length(data.anthropic_workspace_rate_limits.example.rate_limits)
}

output "effective_rate_limit_groups" {
  description = "Rate-limit groups that apply to the workspace, with the ID an override for each one takes."
  value = [
    for rl in data.anthropic_workspace_rate_limits.effective.rate_limits : {
      group         = rl.group.type == "model_group" ? rl.group.display_name : rl.group.type
      rate_limit_id = rl.rate_limit_id
    }
  ]
}
