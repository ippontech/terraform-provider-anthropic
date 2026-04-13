terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

resource "anthropic_message" "example" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 1024

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}

output "response" {
  value = anthropic_message.example.content
}

# Example with system prompt and temperature
resource "anthropic_message" "with_system" {
  model       = "claude-haiku-4-5-20251001"
  max_tokens  = 512
  system      = "You are a helpful assistant that answers questions concisely."
  temperature = 0.7

  messages = [
    {
      role    = "user"
      content = "Explain the greenhouse effect in one sentence."
    }
  ]
}

# Example with multi-turn conversation
resource "anthropic_message" "conversation" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 1024

  messages = [
    {
      role    = "user"
      content = "What is 2+2?"
    },
    {
      role    = "assistant"
      content = "2 + 2 = 4"
    },
    {
      role    = "user"
      content = "What about 3+3?"
    }
  ]
}

output "conversation_response" {
  value = anthropic_message.conversation.content
}
