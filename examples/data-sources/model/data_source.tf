data "anthropic_model" "example" {
  model_id = "claude-sonnet-4-5"
}

output "model" {
  description = "Information about the retrieved Anthropic model."
  value       = data.anthropic_model.example
}

# Warns on every plan once the pinned model is deprecated or retired. A check
# block never blocks the run; a postcondition on the data source fails the plan
# instead.
check "model_is_active" {
  assert {
    condition     = data.anthropic_model.example.lifecycle_stage == "active"
    error_message = "${data.anthropic_model.example.id} is ${data.anthropic_model.example.lifecycle_stage}, retiring at ${coalesce(data.anthropic_model.example.retires_at, "a date not scheduled yet")}."
  }
}
