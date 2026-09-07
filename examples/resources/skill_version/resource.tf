locals {
  # Explicit patterns avoid sweeping the Terraform config itself into the
  # bundle. The provider preserves each file's path relative to the bundle
  # root, so SKILL.md can reference files in subdirectories at runtime.
  bundle_files = [
    for f in setunion(
      fileset(path.module, "SKILL.md"),
      fileset(path.module, "references/**"),
    ) : "${path.module}/${f}"
  ]
}

resource "anthropic_skill" "example" {
  files         = local.bundle_files
  force_destroy = true
}

resource "anthropic_skill_version" "example" {
  skill_id = anthropic_skill.example.id
  files    = local.bundle_files
}

output "skill_version_id" {
  description = "ID of the skill version."
  value       = anthropic_skill_version.example.id
}

output "version" {
  description = "Version identifier of the skill version."
  value       = anthropic_skill_version.example.version
}

output "name" {
  description = "Name of the skill version."
  value       = anthropic_skill_version.example.name
}

output "created_at" {
  description = "Creation timestamp of the skill version (RFC 3339)."
  value       = anthropic_skill_version.example.created_at
}
