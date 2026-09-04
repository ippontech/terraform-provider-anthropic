terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Trust GitHub Actions' OIDC issuer for workload identity federation. The
# issuer URL is publicly reachable over HTTPS, so the default "discovery"
# jwks mode can fetch GitHub's signing keys automatically.
resource "anthropic_federation_issuer" "github_actions" {
  name       = "github-actions"
  issuer_url = "https://token.actions.githubusercontent.com"

  jwks = {
    type = "discovery"
  }

  # GitHub Actions OIDC tokens are short-lived; cap the accepted iat->exp
  # spread accordingly instead of the provider default of 1h.
  max_jwt_lifetime_seconds = 600
}

output "federation_issuer_id" {
  value = anthropic_federation_issuer.github_actions.id
}
