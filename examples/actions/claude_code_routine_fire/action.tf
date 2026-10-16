variable "routine_id" {
  description = "ID of the Claude Code routine to fire (trig_...), from its API trigger URL."
  type        = string
}

variable "routine_token" {
  description = "The routine's API trigger token (sk-ant-oat01-...)."
  type        = string
  sensitive   = true
}

variable "release" {
  description = "Version being deployed; a new value fires the routine again."
  type        = string
  default     = "1.0.0"
}

# Starts a run of the routine. Each invocation creates a new Claude Code session.
action "anthropic_claude_code_routine_fire" "after_deploy" {
  config {
    routine_id = var.routine_id
    token      = var.routine_token
    text       = "Release ${var.release} was applied. Run the smoke checks described in your prompt."
  }
}

# Fires the routine after the release is first applied and on every change.
resource "terraform_data" "release" {
  input = var.release

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.anthropic_claude_code_routine_fire.after_deploy]
    }
  }
}
