terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/MemoryStore/anthropic"
      version = "~> 1.0"
    }
  }
}

data "anthropic_environments" "all" {}

output "environments_count" {
  value = length(data.anthropic_environments.all.environments)
}

output "environment_names" {
  value = [for e in data.anthropic_environments.all.environments : e.name]
}
