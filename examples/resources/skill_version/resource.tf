terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

locals {
  # Explicit patterns avoid sweeping the Terraform config itself into the
  # bundle. The provider preserves each file's path relative to the bundle
  # root, so SKILL.md can reference files in subdirectories at runtime.
  bundle_files = [
    for f in setunion(
      fileset(path.module, "SKILL.md"),
      fileset(path.module, "references/**"),
    ) : "${path.module}/${f}"
  ]
}

resource "anthropic_skill" "example" {
  files         = local.bundle_files
  force_destroy = true
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  files    = local.bundle_files
}

output "skill_version_id" {
  value = anthropic_skill_version.example.id
}

output "version" {
  value = anthropic_skill_version.example.version
}

output "name" {
  value = anthropic_skill_version.example.name
}

output "created_at" {
  value = anthropic_skill_version.example.created_at
}
