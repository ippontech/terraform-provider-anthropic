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
  # fileset() picks up SKILL.md and any nested files (e.g. references/*.md).
  # The provider preserves each file's path relative to the bundle root so
  # SKILL.md can reference files in subdirectories at runtime.
  files         = [for f in fileset(path.module, "**") : "${path.module}/${f}"]
  force_destroy = true
}

output "skill_id" {
  value = anthropic_skill.example.id
}

output "skill_source" {
  value = anthropic_skill.example.source
}
