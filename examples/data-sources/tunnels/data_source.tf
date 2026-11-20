data "anthropic_tunnels" "live" {}

data "anthropic_tunnels" "all" {
  include_archived = true
}

output "tunnel_domains" {
  description = "Domains of the workspace's live tunnels, by tunnel ID."
  value       = { for t in data.anthropic_tunnels.live.tunnels : t.id => t.domain }
}

output "archived_tunnel_count" {
  description = "Number of archived tunnels in the workspace."
  value       = length([for t in data.anthropic_tunnels.all.tunnels : t if t.archived_at != null])
}
