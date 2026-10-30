# What applies in each workspace once the Slack defaults are taken into
# account, to audit it.
data "anthropic_claude_tag_slack_workspaces" "all" {
  include_inherited = true
}

output "workspaces_open_to_guests" {
  description = "Slack IDs of the workspaces whose guest policy resolves to allow."
  value = [
    for workspace in data.anthropic_claude_tag_slack_workspaces.all.slack_workspaces : workspace.slack.workspace_id
    if workspace.guest_policy == "allow"
  ]
}
