terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Minimal example: count tokens for a simple user message
data "anthropic_count_tokens" "simple" {
  model = "claude-haiku-4-5-20251001"

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}

output "simple_token_count" {
  description = "Number of tokens for a simple user message."
  value       = data.anthropic_count_tokens.simple.input_tokens
}

# Example with system prompt
data "anthropic_count_tokens" "with_system" {
  model  = "claude-haiku-4-5-20251001"
  system = "You are a helpful assistant that answers questions concisely."

  messages = [
    {
      role    = "user"
      content = "Explain the greenhouse effect in one sentence."
    }
  ]
}

output "tokens_with_system" {
  description = "Number of tokens including the system prompt overhead."
  value       = data.anthropic_count_tokens.with_system.input_tokens
}

# Example with multi-turn conversation
data "anthropic_count_tokens" "conversation" {
  model = "claude-haiku-4-5-20251001"

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    },
    {
      role    = "assistant"
      content = "The capital of France is Paris."
    },
    {
      role    = "user"
      content = "What is its population?"
    }
  ]
}

output "conversation_token_count" {
  description = "Number of tokens for a multi-turn conversation."
  value       = data.anthropic_count_tokens.conversation.input_tokens
}
