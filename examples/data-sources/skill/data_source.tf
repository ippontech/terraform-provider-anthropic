terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

resource "anthropic_skill" "example" {
  display_title = "Example Skill (data source)"
  files         = ["${path.module}/SKILL.md"]
}

data "anthropic_skill" "example" {
  skill_id = anthropic_skill.example.id
}

output "skill" {
  description = "Information about the retrieved Anthropic skill."
  value       = data.anthropic_skill.example
}
