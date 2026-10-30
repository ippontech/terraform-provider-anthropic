# Settings every channel of a connected workspace inherits unless it sets its
# own; channels add their instructions to these.
resource "anthropic_claude_tag_slack_workspace_settings" "main" {
  slack = {
    workspace_id = "T01ABCD2EFG"
  }

  instructions = "Answer in English unless asked otherwise."
  guest_policy = "restrict"
}

output "workspace_id" {
  description = "Claude Tag ID of the workspace (slws_...)."
  value       = anthropic_claude_tag_slack_workspace_settings.main.id
}
