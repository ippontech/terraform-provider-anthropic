terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

resource "anthropic_skill" "example" {
  files         = [for f in fileset(path.module, "**") : "${path.module}/${f}"]
  force_destroy = true
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  # fileset() picks up SKILL.md and any nested files (e.g. references/*.md).
  # The provider preserves each file's path relative to the bundle root so
  # SKILL.md can reference files in subdirectories at runtime.
  files = [for f in fileset(path.module, "**") : "${path.module}/${f}"]
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
