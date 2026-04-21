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
  files = ["${path.module}/SKILL.md"]
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  files    = ["${path.module}/SKILL.md"]
}

output "skill_version_id" {
  value = anthropic_skill_version.example.id
}

output "version" {
  value = anthropic_skill_version.example.version
}
