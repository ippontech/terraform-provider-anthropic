data "anthropic_model" "example" {
  model_id = "claude-sonnet-4-5"
}

# Warns on every plan and apply once the pinned model is deprecated or retired,
# without stopping them.
check "model_is_active" {
  assert {
    condition     = data.anthropic_model.example.lifecycle_stage == "active"
    error_message = "${data.anthropic_model.example.id} is ${data.anthropic_model.example.lifecycle_stage}, retiring at ${coalesce(data.anthropic_model.example.retires_at, "a date not scheduled yet")}."
  }
}
