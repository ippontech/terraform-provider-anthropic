terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

variable "source_filter" {
  description = "Optional filter by skill source. Accepted values: \"custom\" or \"anthropic\"."
  type        = string
  default     = null
}

resource "anthropic_skill" "example" {
  display_title = "Example Skill (list)"
  files         = ["${path.module}/SKILL.md"]
}

data "anthropic_skills" "all" {
  source_filter = var.source_filter
  depends_on    = [anthropic_skill.example]
}

output "skills" {
  description = "List of available Anthropic skills."
  value       = data.anthropic_skills.all.skills
}
