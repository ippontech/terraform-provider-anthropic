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
  display_title = "Example Skill (list)"
  files         = ["${path.module}/SKILL.md"]
}

data "anthropic_skills" "all" {
  depends_on = [anthropic_skill.example]
}

output "skills" {
  description = "List of available Anthropic skills."
  value       = data.anthropic_skills.all.skills
}
