variable "order" {
  description = "Optional sort order for results, by creation time (`asc` or `desc`)."
  type        = string
  default     = null
}

data "anthropic_user_profiles" "all" {
  order = var.order
}

output "user_profiles" {
  description = "List of user profiles."
  value       = data.anthropic_user_profiles.all.user_profiles
  sensitive   = true
}
