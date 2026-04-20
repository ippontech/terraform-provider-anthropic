terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Create an agent, then look it up via the data source.
resource "anthropic_agent" "example" {
  model = "claude-sonnet-4-6"
  name  = "Example Agent (data source)"
}

data "anthropic_agent" "example" {
  agent_id = anthropic_agent.example.id
}

output "agent" {
  description = "Information about the retrieved Anthropic agent."
  value       = data.anthropic_agent.example
}
