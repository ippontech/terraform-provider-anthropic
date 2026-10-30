# Turn Claude on in one approved channel, keyed by the IDs Slack shows. With
# Claude off in the organization's Slack defaults, it answers only in the
# channels set up this way.
resource "anthropic_claude_tag_slack_channel" "support" {
  slack = {
    workspace_id = "T01ABCD2EFG"
    channel_id   = "C05HJKL6MNP"
  }

  enabled      = true
  model        = "claude-opus-5-5"
  guest_policy = "restrict"
  instructions = <<-EOT
    Answer in the language of the question, and link the runbook you used.
  EOT
}

output "support_channel_id" {
  description = "Claude Tag ID of the support channel (slch_...)."
  value       = anthropic_claude_tag_slack_channel.support.id
}
