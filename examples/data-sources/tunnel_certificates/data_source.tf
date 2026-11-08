data "anthropic_tunnel_certificates" "trusted" {
  tunnel_id = "tnl_01AAAAAAAAAAAAAAAAAAAAAA"
}

data "anthropic_tunnel_certificates" "all" {
  tunnel_id        = "tnl_01AAAAAAAAAAAAAAAAAAAAAA"
  include_archived = true
}

output "trusted_certificate_expiry" {
  description = "Fingerprint and expiry (RFC 3339, null if none) of each CA certificate the tunnel trusts, by certificate ID."
  value = {
    for c in data.anthropic_tunnel_certificates.trusted.certificates : c.id => {
      fingerprint = c.fingerprint
      expires_at  = c.expires_at
    }
  }
}

output "archived_certificate_count" {
  description = "Number of archived CA certificates on the tunnel."
  value       = length([for c in data.anthropic_tunnel_certificates.all.certificates : c if c.archived_at != null])
}
