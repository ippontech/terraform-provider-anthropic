data "anthropic_tunnel" "example" {
  id = "tnl_01AAAAAAAAAAAAAAAAAAAAAA"
}

output "tunnel_domain" {
  description = "Domain of the tunnel."
  value       = data.anthropic_tunnel.example.domain
}
