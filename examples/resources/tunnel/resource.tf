resource "anthropic_tunnel" "corp" {
  display_name = "corp-mcp"

  # Change this value (to "2", a date, ...) to rotate the connector token.
  token_rotation = "1"
}

output "tunnel_domain" {
  description = "Domain of the tunnel: MCP servers behind it are reached at https://<route>.<domain>/."
  value       = anthropic_tunnel.corp.domain
}

output "tunnel_token" {
  description = "Connector token cloudflared runs the tunnel with."
  value       = anthropic_tunnel.corp.token
  sensitive   = true
}
