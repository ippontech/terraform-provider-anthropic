terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Create a skill and a version so the list is guaranteed non-empty for tests.
resource "anthropic_skill" "example" {
  files         = ["${path.module}/SKILL.md"]
  force_destroy = true
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  files    = ["${path.module}/SKILL.md"]
}

data "anthropic_skill_versions" "example" {
  skill_id = anthropic_skill.example.id

  # Ensure the freshly-created skill version is included in the result.
  depends_on = [anthropic_skill_version.example]
}

output "skill_versions" {
  description = "List of Skill Versions for the example Skill."
  value       = data.anthropic_skill_versions.example.versions
}
