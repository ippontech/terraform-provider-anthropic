terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

resource "anthropic_skill" "example" {
  files = ["${path.module}/SKILL.md"]
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  files    = ["${path.module}/SKILL.md"]
}

data "anthropic_skill_version" "example" {
  skill_id = anthropic_skill_version.example.skill_id
  version  = anthropic_skill_version.example.version
}

output "skill_version" {
  description = "Information about the retrieved Anthropic skill version."
  value       = data.anthropic_skill_version.example
}
