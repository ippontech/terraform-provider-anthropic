terraform {
  required_version = ">= 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

variable "environment_id" {
  type        = string
  description = "The ID of the environment to look up."
}

data "anthropic_environment" "example" {
  environment_id = var.environment_id
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
