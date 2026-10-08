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
  description = "Content blocks of the model's reply."
  value       = anthropic_message.example.content
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
  description = "Content blocks of the reply to the multi-turn conversation."
  value       = anthropic_message.conversation.content
}

# Example with extended thinking, prompt caching, stop sequences and metadata
resource "anthropic_message" "with_thinking" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 2048
  system     = "You are a careful assistant. Answer in one short sentence."

  # Place a (5 minute) cache breakpoint on the last cacheable block; this
  # also covers the system prompt, which precedes it.
  cache_control = {}

  # `enabled` needs budget_tokens (at least 1024, below max_tokens).
  # Newer models also accept type = "adaptive". `type = "disabled"` is rejected
  # by some models: omit the block instead.
  thinking = {
    type          = "enabled"
    budget_tokens = 1024
  }

  stop_sequences = ["END_OF_ANSWER"]

  metadata = {
    user_id = "terraform-example-user"
  }

  messages = [
    {
      role    = "user"
      content = "What is 17 multiplied by 23?"
    }
  ]
}

output "thinking_tokens" {
  description = "Output tokens the model spent on internal reasoning."
  value       = anthropic_message.with_thinking.thinking_tokens
}

# Example with structured output: the reply is JSON matching the schema
resource "anthropic_message" "structured" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 256

  output_config = {
    format = jsonencode({
      type = "object"
      properties = {
        capital = { type = "string" }
        country = { type = "string" }
      }
      required             = ["capital", "country"]
      additionalProperties = false
    })
  }

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}

output "structured_capital" {
  description = "The capital extracted from the structured JSON reply."
  value       = jsondecode(anthropic_message.structured.content).capital
}
