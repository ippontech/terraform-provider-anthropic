# Agent and environment the deployment binds together.
resource "anthropic_agent" "assistant" {
  model = "claude-sonnet-4-6"
  name  = "Deployment Example Agent"
}

resource "anthropic_environment" "sessions" {
  name = "deployment-example-environment"
}

# Minimal deployment: no schedule, runs only when triggered manually.
resource "anthropic_deployment" "minimal" {
  name           = "minimal-deployment"
  agent_id       = anthropic_agent.assistant.id
  environment_id = anthropic_environment.sessions.id
  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        {
          type = "text"
          text = "Summarize the latest activity."
        }
      ]
    }
  ])
}

# Scheduled deployment with metadata and a spend budget.
resource "anthropic_deployment" "scheduled" {
  name           = "scheduled-deployment"
  description    = "Runs every weekday morning"
  agent_id       = anthropic_agent.assistant.id
  environment_id = anthropic_environment.sessions.id

  initial_events = jsonencode([
    {
      type = "user.message"
      content = [
        {
          type = "text"
          text = "Run the daily report."
        }
      ]
    }
  ])

  schedule = {
    expression = "0 9 * * 1-5"
    timezone   = "UTC"
  }

  budget = jsonencode({
    max_list_cost = {
      amount   = "2500"
      currency = "USD"
    }
    type = "limit"
  })

  metadata = {
    team = "platform"
  }
}

output "deployment_id" {
  description = "ID of the minimal deployment."
  value       = anthropic_deployment.minimal.id
}

output "deployment_status" {
  description = "Lifecycle status of the minimal deployment."
  value       = anthropic_deployment.minimal.status
}
