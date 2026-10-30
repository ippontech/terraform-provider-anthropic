---
page_title: "anthropic_claude_tag_slack_channel Resource - anthropic"
subcategory: "Claude Enterprise"
description: |-
  Manages the Claude Tag settings of one Slack channel: whether Claude is on, its instructions, model, environment and guest policy.
---

# anthropic_claude_tag_slack_channel (Resource)

Manages the Claude Tag settings of one Slack channel, through the [Claude Tag API](https://platform.claude.com/docs/en/manage-claude/claude-tag-api#slack-channels): the five settings a channel can store (`enabled`, `instructions`, `model`, `environment_id`, `guest_policy`), keyed by the IDs Slack shows for the channel (`slack.workspace_id` and `slack.channel_id`, the shape of the API's `slack` object). This is the guide's [Keep configuration in version control](https://platform.claude.com/docs/en/manage-claude/claude-tag-api#keep-configuration-in-version-control) workflow: the provider finds the channel by its Slack IDs, compares what it stores with the configuration, and sends only the settings that differ.

> **API**: `POST /v1/organizations/claude_tag/slack_channels` (set up), `GET|POST|DELETE /v1/organizations/claude_tag/slack_channels/{id}`, `GET /v1/organizations/claude_tag/slack_channels?slack_workspace_id=...&slack_channel_id=...` (find) (Claude Tag API).
> **Auth**: Claude Enterprise scoped Admin API key with the `read:claude_tag` and `write:claude_tag` scopes (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`), created by the primary owner in claude.ai under Organization settings > API. A Claude Console Admin API key does not work. The key must belong to the organization as a whole.
> **Beta header**: `experimental-claude-tag-2026-10-21`, sent by the provider (the endpoints answer 404 without it).
> **Availability**: beta, Claude Enterprise organizations with Claude Tag set up and a Slack workspace or Enterprise Grid connected; not Claude Console organizations.
> **Rate limits**: two budgets per organization, shared by all its keys: reads (every `GET`) and changes (every `POST` and `DELETE`). A `429` is retried after its `retry-after`.

## Behavior

- **Creating it adopts a channel that is already set up.** Create finds the channel by `slack.workspace_id` and `slack.channel_id`. A channel set up in claude.ai, or by Claude's first use in it, is adopted with its `slch_` ID; otherwise the channel is set up. Either way only the settings that differ from what the channel stores are sent, so nothing is written when it already stores the configuration. Setting a channel up needs Claude's Slack app installed in the workspace and a channel that exists, is not archived and is visible to the app; it also sets up a workspace of an Enterprise Grid that was not.
- **The configuration owns the five settings.** A setting the configuration does not declare is cleared on the channel (sent as `null`), so the channel inherits it from its workspace or the organization's Slack defaults. A setting changed in claude.ai or in Slack shows as drift and the next apply sets it back. Ask an organization owner to turn on **API / Terraform read-only mode** in claude.ai's Claude Tag settings so that nobody changes them by hand: it makes claude.ai and Slack refuse a person's changes, not the API's.
- **Changing `slack` sets up another channel**: the resource is replaced, and the first channel is removed (which needs Claude off in it, see below).
- **Settings are checked at plan where the API would refuse them**: blank `instructions` (the API takes them as clearing the setting), `instructions` over 8,192 characters once trimmed, `model` over 128 characters, an unknown `guest_policy`, Slack IDs of the wrong form. `model` and `environment_id` are checked by the API when written, and not again. The API stores `instructions` without leading and trailing white space; the configuration's text (a heredoc's final newline, say) is kept in the state.
- **Environments and runner pools.** `environment_id` takes an active environment (`env_...`) of the Claude Enterprise organization. Whether an environment created with `anthropic_environment` in a Claude Console workspace is accepted is not verified. An `environment_id` holding a self-hosted runner pool (`ccpool_...`), set by an owner in claude.ai, is kept but cannot be set, changed or cleared through the API: the provider reports a change before sending anything, and declaring the pool's ID keeps it.
- **Destroying it removes the channel from Claude Tag**, which deletes its settings, Bundle attachments, sessions and their transcripts, memory, routines and published artifacts, and cannot be undone; the channel in Slack is untouched. Claude Tag removes a channel only while Claude is off in it: set `enabled = false` and apply first, otherwise the destroy fails with `slack_channel_enabled`. Removal does not keep Claude out of the channel: if what it inherits turns Claude on, Claude's next mention sets it up again. To keep Claude out, keep the resource with `enabled = false`; to stop managing the channel without removing it, use a [`removed` block](https://developer.hashicorp.com/terraform/language/block/removed). A group direct message, and a channel holding configuration the API does not manage (a runner pool, say), cannot be removed through the API.
- **A channel removed elsewhere** (in claude.ai, by deleting it in Slack, or replaced in claude.ai while read-only mode is off) answers 404 on refresh. The provider then lists the workspaces with the same key: when that succeeds, the channel is gone and the next apply sets it up again with a new ID; when it also answers 404, the Claude Tag API itself is unreachable for the key, and the refresh fails instead of dropping the channel from the state.

## Example Usage

```hcl
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `slack` (Attributes) Slack's own IDs of the channel, the ones Slack shows (the API's `slack` object). Changing either sets up another channel. (see [below for nested schema](#nestedatt--slack))

### Optional

- `enabled` (Boolean) Whether Claude is on in the channel (claude.ai: **Enable Claude Tag in Slack**). Omitted, the setting is cleared on the channel, which then inherits it.
- `environment_id` (String) The cloud environment sessions in the channel run in: an active environment (`env_...`) of the Claude Enterprise organization. Checked when written, not again. A self-hosted runner pool (`ccpool_...`) set in claude.ai is kept, but cannot be set, changed or cleared through the API. Whether an environment created with `anthropic_environment` in a Claude Console workspace is accepted here is not verified. Declare a runner pool's ID to keep it. Omitted, the setting is cleared on the channel, which then inherits it.
- `guest_policy` (String) What Claude does in a channel while a Slack guest is in it: `allow` (claude.ai: **Full access**), `restrict` (**Restrict**) or `channel_only` (**Channel only**; refused when not available to the organization). Omitted, the setting is cleared on the channel, which then inherits it.
- `instructions` (String) Custom instructions Claude reads in every session in the channel. At most 8,192 characters after leading and trailing white space is trimmed; blank text is refused here (omit the attribute to clear the setting). Instructions are additive: the text of every level that sets them is joined, broadest first. Omitted, the setting is cleared on the channel, which then inherits it.
- `model` (String) The model new sessions in the channel start on: a model ID the organization can use with Claude Tag, at most 128 characters. Checked when written, not again. Omitted, the setting is cleared on the channel, which then inherits it.

### Read-Only

- `created_at` (String) RFC 3339 timestamp of when the channel was set up.
- `id` (String) The channel's Claude Tag ID (prefixed `slch_`).
- `updated_at` (String) RFC 3339 timestamp of the last change to Claude Tag's record of the channel, here, in claude.ai or in Slack. Attaching or detaching a Bundle does not move it.

<a id="nestedatt--slack"></a>
### Nested Schema for `slack`

Required:

- `channel_id` (String) Slack's own ID of the channel (`C...` or `G...`). Not the `slch_` ID.
- `workspace_id` (String) Slack's own ID of the workspace the channel is in (`T...`), or of an Enterprise Grid (`E...`) for a channel shared with the whole Grid. Not the `slws_` ID.

## Import

Import a set-up channel by its Claude Tag ID (`slch_...`), which `anthropic_claude_tag_slack_channel` (data source) finds from the Slack IDs in its `slack` attribute:

```shell
terraform import anthropic_claude_tag_slack_channel.support slch_011CZkZmRt4vNq8sHbYw2KdX
```
