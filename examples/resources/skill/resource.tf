terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

resource "anthropic_skill" "example" {
  display_title = "Example Skill"
  files         = ["${path.module}/SKILL.md"]
}

output "skill_id" {
  value = anthropic_skill.example.id
}

output "skill_source" {
  value = anthropic_skill.example.source
}
