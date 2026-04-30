terraform {
  required_version = ">= 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

# Create an environment to read with the data source
resource "anthropic_environment" "created" {
  name = "environment-data-source-example"
}

# Look up the created environment by ID
data "anthropic_environment" "example" {
  environment_id = anthropic_environment.created.id
}

output "environment_id" {
  value = data.anthropic_environment.example.id
}

output "environment_name" {
  value = data.anthropic_environment.example.name
}

output "environment_type" {
  value = data.anthropic_environment.example.type
}
