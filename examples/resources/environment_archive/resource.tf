terraform {
  required_version = ">= 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

resource "anthropic_environment" "example" {
  name        = "environment-to-archive"
  description = "This environment will be archived"
}

resource "anthropic_environment_archive" "example" {
  environment_id = anthropic_environment.example.id
}

output "environment_archive_id" {
  value = anthropic_environment_archive.example.id
}

output "archived_at" {
  value = anthropic_environment_archive.example.archived_at
}
