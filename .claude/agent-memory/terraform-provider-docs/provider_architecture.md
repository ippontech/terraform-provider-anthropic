---
name: anthropic provider architecture and patterns
description: Key architectural decisions and resource patterns in the terraform-provider-anthropic
type: reference
---

## Provider Overview

- **Registry:** `registry.terraform.io/ippontech/anthropic`
- **Framework:** HashiCorp Terraform Plugin Framework v1.13.0
- **Entry point:** `main.go`
- **Provider configuration:** `internal/provider/provider.go`
- **Resource/DataSource location:** `internal/provider/`
- **Example configs:** `examples/resources/` and `examples/data-sources/`
- **Documentation:** Auto-generated from `templates/` via `make generate`

## anthropic_message Resource Pattern

**Type:** Write-only, immutable resource (ephemeral pattern)

**Key characteristics:**
- No persistent remote state to read/update/delete
- All inputs trigger resource replacement (RequiresReplace plan modifiers on every input)
- Create operation: calls `POST /v1/messages` API, stores response in Terraform state
- Read operation: no-op (API has no GET endpoint)
- Update operation: never called (immutable)
- Delete operation: no-op (API has no DELETE endpoint)

**Input attributes:**
- `model` (Required, String) — model ID, triggers replacement
- `max_tokens` (Required, Int64) — token limit, triggers replacement
- `messages` (Required, List) — conversational messages with nested role/content, triggers replacement
- `system` (Optional, String) — system prompt, triggers replacement
- `temperature` (Optional, Float64) — randomness 0.0-1.0, triggers replacement

**Computed attributes:**
- `id` — API-assigned message identifier
- `content` — generated text response
- `stop_reason` — reason generation stopped (e.g., `end_turn`, `max_tokens`)
- `input_tokens` — token count of input
- `output_tokens` — token count of output

**Documentation patterns:**
- Include a note explaining the immutable, write-only nature
- Import section states: "does not support import" with explanation
- Examples should demonstrate single message, with optional system/temperature, and multi-turn conversation
- Schema descriptions should be complete but concise
