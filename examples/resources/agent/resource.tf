# Minimal agent. model_effort is optional: when omitted, the API resolves a
# per-model default and stores it in state.
resource "anthropic_agent" "simple" {
  model        = "claude-sonnet-4-6"
  name         = "Simple Agent"
  model_effort = "high"
}

# Agent with system prompt and description
resource "anthropic_agent" "assistant" {
  model       = "claude-sonnet-4-6"
  name        = "Customer Support Agent"
  description = "Handles customer support inquiries"
  system      = "You are a helpful customer support agent. Be concise and friendly."

  metadata = {
    team = "support"
    env  = "production"
  }
}

# Agent with built-in toolset configuration
resource "anthropic_agent" "developer" {
  model       = "claude-sonnet-4-6"
  name        = "Developer Agent"
  description = "A coding assistant with file and web access"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
    configs = [
      {
        name              = "bash"
        enabled           = true
        permission_policy = "always_ask"
      }
    ]
  }
}

# Agent with MCP server and skills
resource "anthropic_agent" "mcp_agent" {
  model       = "claude-sonnet-4-6"
  name        = "MCP Agent"
  description = "An agent connected to an MCP server"

  mcp_servers = [
    {
      name = "my_server"
      url  = "https://mcp.example.com/sse"
    }
  ]

  mcp_toolsets = [
    {
      mcp_server_name           = "my_server"
      default_enabled           = true
      default_permission_policy = "always_allow"
    }
  ]

  skills = [
    {
      type     = "anthropic"
      skill_id = "xlsx"
    }
  ]
}

# Agent with custom tools
resource "anthropic_agent" "custom_tools" {
  model       = "claude-sonnet-4-6"
  name        = "Custom Tool Agent"
  description = "An agent with a custom lookup tool"

  custom_tools = [
    {
      name        = "lookup_user"
      description = "Look up a user by their email address"
      input_schema = jsonencode({
        type = "object"
        properties = {
          email = {
            type        = "string"
            description = "The user's email address"
          }
        }
        required = ["email"]
      })
    }
  ]
}

# Coordinator agent that can delegate to another agent and to itself.
# The members must not have their own multiagent roster (depth limit 1).
resource "anthropic_agent" "coordinator" {
  model       = "claude-sonnet-4-6"
  name        = "Support Coordinator"
  description = "Delegates support inquiries to the support agent"

  multiagent = {
    type = "coordinator"
    agents = [
      {
        type = "agent"
        id   = anthropic_agent.assistant.id
      },
      {
        type = "self"
      },
    ]
  }
}

output "simple_agent_id" {
  description = "ID of the minimal agent."
  value       = anthropic_agent.simple.id
}

output "simple_agent_model_effort" {
  description = "Effort level of the minimal agent."
  value       = anthropic_agent.simple.model_effort
}

output "developer_agent_version" {
  description = "Version number of the developer agent."
  value       = anthropic_agent.developer.version
}

output "coordinator_roster_size" {
  description = "Number of entries in the coordinator agent's roster."
  value       = length(anthropic_agent.coordinator.multiagent.agents)
}
