data "anthropic_federation_issuer" "example" {
  id = "fdis_01ABCDEFabcdef0123456789XY"
}

output "federation_issuer_name" {
  description = "Name of the federation issuer."
  value       = data.anthropic_federation_issuer.example.name
}

output "federation_issuer_issuer_url" {
  description = "OIDC issuer URL matched against the JWT `iss` claim."
  value       = data.anthropic_federation_issuer.example.issuer_url
}
