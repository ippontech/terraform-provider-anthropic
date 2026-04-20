terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Create an agent so the list is guaranteed non-empty for tests.
resource "anthropic_agent" "example" {
  model = "claude-sonnet-4-6"
  name  = "Example Agent (for list data source)"
}

data "anthropic_agents" "example" {
  # Ensure the freshly-created agent is included in the result.
  depends_on = [anthropic_agent.example]
}

output "agents" {
  description = "List of Anthropic Managed Agents."
  value       = data.anthropic_agents.example.agents
}
