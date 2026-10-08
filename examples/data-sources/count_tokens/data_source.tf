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

# Example with extended thinking: the count includes the thinking overhead
data "anthropic_count_tokens" "with_thinking" {
  model = "claude-haiku-4-5-20251001"

  thinking = {
    type          = "enabled"
    budget_tokens = 1024
  }

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}

output "tokens_with_thinking" {
  description = "Number of tokens for the simple message with extended thinking enabled."
  value       = data.anthropic_count_tokens.with_thinking.input_tokens
}

# Example with a structured-output schema and a prompt-caching breakpoint
data "anthropic_count_tokens" "with_output_format" {
  model = "claude-haiku-4-5-20251001"

  output_config = {
    format = jsonencode({
      type = "object"
      properties = {
        capital = { type = "string" }
      }
      required             = ["capital"]
      additionalProperties = false
    })
  }

  cache_control = {
    ttl = "1h"
  }

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}

output "tokens_with_output_format" {
  description = "Number of tokens for the simple message with a JSON-schema output format."
  value       = data.anthropic_count_tokens.with_output_format.input_tokens
}
