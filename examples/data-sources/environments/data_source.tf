data "anthropic_environments" "all" {}

output "environments_count" {
  value = length(data.anthropic_environments.all.environments)
}

output "environment_names" {
  value = [for e in data.anthropic_environments.all.environments : e.name]
}
