data "anthropic_federation_issuer" "example" {
  id = "fdis_01ABCDEFabcdef0123456789XY"
}

output "federation_issuer_name" {
  value = data.anthropic_federation_issuer.example.name
}

output "federation_issuer_issuer_url" {
  value = data.anthropic_federation_issuer.example.issuer_url
}
