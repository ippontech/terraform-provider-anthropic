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
  display_title = "Example Skill"
  # Explicit patterns avoid sweeping the Terraform config itself into the
  # bundle. The provider preserves each file's path relative to the bundle
  # root, so SKILL.md can reference files in subdirectories at runtime.
  files = [
    for f in setunion(
      fileset(path.module, "SKILL.md"),
      fileset(path.module, "references/**"),
    ) : "${path.module}/${f}"
  ]
  force_destroy = true
}

output "skill_id" {
  value = anthropic_skill.example.id
}

output "skill_source" {
  value = anthropic_skill.example.source
}
