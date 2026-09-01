terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

# Minimal agent
resource "anthropic_agent" "simple" {
  model        = "claude-sonnet-4-6"
  model_effort = "high"
  name         = "Simple Agent"
}

# Coordinator agent that can delegate to another managed agent
resource "anthropic_agent" "coordinator" {
  model        = "claude-sonnet-4-6"
  model_effort = "high"
  name         = "Support Coordinator"

  multiagent = {
    type = "coordinator"
    agents = [
      {
        type = "self"
      },
      {
        type    = "agent"
        id      = anthropic_agent.assistant.id
        version = anthropic_agent.assistant.version
      }
    ]
  }
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

output "simple_agent_id" {
  value = anthropic_agent.simple.id
}

output "developer_agent_version" {
  value = anthropic_agent.developer.version
}
