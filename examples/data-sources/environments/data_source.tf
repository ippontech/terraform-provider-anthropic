data "anthropic_environments" "all" {}

output "environments_count" {
  description = "Number of environments in the workspace."
  value       = length(data.anthropic_environments.all.environments)
}

output "environment_names" {
  description = "Names of all environments in the workspace."
  value       = [for e in data.anthropic_environments.all.environments : e.name]
}
