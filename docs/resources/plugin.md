---
page_title: "anthropic_plugin Resource - anthropic"
subcategory: "Claude Enterprise"
description: |-
  Uploads an organization-owned plugin to a Claude Enterprise organization and chooses the version members are served.
---

# anthropic_plugin (Resource)

Uploads an organization-owned plugin to a Claude Enterprise organization through the [Plugins API](https://platform.claude.com/docs/en/manage-claude/plugins-api), chooses the version members are served, and deletes the plugin on destroy. A plugin bundles skills, commands, agents, hooks, MCP servers and CLIs, described by its manifest at `.claude-plugin/plugin.json`.

> **API**: `POST /v1/organizations/plugins`, `GET/POST/DELETE /v1/organizations/plugins/{plugin_id}` (Plugins API).
> **Auth**: Claude Enterprise Admin API key with the `read:plugins` and `write:plugins` scopes (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`), created by the primary owner in claude.ai under Organization settings > API. A Claude Console Admin API key does not work.
> **Beta header**: `ce-plugins-2026-09-01`, sent by the provider (the endpoints answer 404 without it).
> **Availability**: beta, Claude Enterprise organizations only, and not organizations with HIPAA readiness enabled.
> **Cost**: Free.

## Behavior

- **Create** uploads the plugin and its first version from `source_dir` (every regular file under the directory, under its path within it, except Git metadata: `.git` directories and files at any depth are left out) or `source_archive` (one `.zip` or `.plugin` archive), into `marketplace_id` or the organization's library marketplace. The upload must meet the [upload requirements](https://platform.claude.com/docs/en/manage-claude/plugins-api#upload-requirements); the manifest's `name` becomes the plugin's `name`, unique within the marketplace. The upload is not idempotent, so the provider never resends it after a server error.
- **Later content goes through `anthropic_plugin_version`.** `source_dir`, `source_archive` and `release_notes` are read only when the plugin is created: changing them afterwards uploads nothing, and the plan says so with a warning.
- **`served_version_id` pins the plugin.** Left unset, members are served the newest version. Setting it serves that version and pins the plugin: later uploads are stored but not served until `served_version_id` names them, and a pinned plugin cannot be unpinned. Removing the attribute leaves the pin where it is. Because an `anthropic_plugin_version` refers to its plugin, it cannot be referenced back from `served_version_id` (Terraform would report a cycle): pass the version ID in, for example through a variable your pipeline sets once it has tested the version. Set it only once the plugin exists: a new plugin has no version yet, so a plan that creates the plugin fails while `served_version_id` is set, including a replacement (a `marketplace_id` change, or a plugin deleted outside Terraform), whose configured ID belongs to the previous plugin. With `ignore_changes` covering `served_version_id` (or `all`), Terraform plans with the served version in the configuration but applies the configuration as written: on an unpinned plugin whose configuration does not set `served_version_id`, every plan shows an in-place update with `served_version_pinned` and every value the API computes (`latest_version_id`, `updated_at`, the display fields, `content_scan`, `components`, `reach`, `created_by`, the installation preference) as known after apply, so whatever reads them changes too, and the apply leaves the plugin unpinned. An unset `served_version_id` is never changed by Terraform, so it needs no `ignore_changes`: list the other attributes instead of `all`, set `served_version_id`, or pin the plugin in claude.ai to end it.
- **Content scanning.** When the organization scans plugin content, members are served a version only once its scan is `completed` with `pass` or `warn`. Serving a version whose scan is still running fails (`scan_pending`: apply again later), and one whose scan failed is refused.
- **Destroy deletes the plugin permanently**, with every version it holds and its installation settings. To withhold a plugin reversibly instead, set its organization-wide installation setting to `not_available`.
- A plugin in a marketplace synchronized from Git cannot receive uploads or be deleted here: change the repository instead.

## Example Usage

```hcl
# Uploads the plugin and its first version from the plugin's root directory
# (its manifest is at sales-toolkit/.claude-plugin/plugin.json). Publish later
# content with anthropic_plugin_version.
resource "anthropic_plugin" "example" {
  source_dir    = "${path.module}/sales-toolkit"
  release_notes = "First release"

  # Once the plugin exists, to choose the version members are served (roll
  # back or promote), set its ID. This pins the plugin, which cannot be undone.
  # served_version_id = "pluginver_01Km7tL4pR9xF5sU2zV3jP6q"
}

output "plugin_id" {
  description = "ID of the plugin."
  value       = anthropic_plugin.example.id
}

output "plugin_name" {
  description = "Name of the plugin, from its manifest."
  value       = anthropic_plugin.example.name
}

output "served_version_id" {
  description = "Version members are served."
  value       = anthropic_plugin.example.served_version_id
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `marketplace_id` (String) ID of the organization-owned `manual` marketplace to create the plugin in (prefixed `marketplace_`). Defaults to the organization's library marketplace, which the first upload creates. Changing it forces a new plugin.
- `release_notes` (String) Release notes of the first version, shown in its version history in claude.ai (up to 5,000 characters). Read only when the plugin is created: a later change is recorded in state but uploads nothing (Terraform shows a warning). Publish new content with `anthropic_plugin_version`.
- `served_version_id` (String) The version members are served (prefixed `pluginver_`). Left unset, it follows the newest version until the plugin is pinned. Setting it **pins** the plugin: later uploads are stored but not served until this value names them, and a pinned plugin cannot be unpinned (removing the attribute leaves the pin where it is). Set an earlier version to roll back, a newer one to promote it. Because `anthropic_plugin_version` refers to its plugin, a version resource cannot be referenced here (that would be a dependency cycle): pass the version ID in, from a variable for example. Set it only once the plugin exists: a plan that creates the plugin, a replacement included, fails while it is set. With content scanning on, a version whose scan is still running or failed is refused.
- `source_archive` (String) Local `.zip` or `.plugin` archive holding the plugin (it may wrap everything in one top-level folder). Conflicts with `source_dir`. Read only when the plugin is created: a later change is recorded in state but uploads nothing (Terraform shows a warning). Publish new content with `anthropic_plugin_version`.
- `source_dir` (String) Local directory holding the plugin, with its manifest at `.claude-plugin/plugin.json`. Every regular file under it is uploaded under its path within the directory, except Git metadata (`.git`, at any depth), so point it at the plugin's root (a Git checkout of the plugin alone is fine); symbolic links are refused. Conflicts with `source_archive`; one of them is required to create a plugin. Read only when the plugin is created: a later change is recorded in state but uploads nothing (Terraform shows a warning). Publish new content with `anthropic_plugin_version`.

### Read-Only

- `components` (Attributes List) The served version's components, null when not recorded. (see [below for nested schema](#nestedatt--components))
- `content_scan` (Attributes) The served version's content scan, null when it was never scanned. Members are served a version only once its scan is `completed` with `pass` or `warn`. (see [below for nested schema](#nestedatt--content_scan))
- `created_at` (String) RFC 3339 timestamp of the plugin's creation.
- `created_by` (Attributes) Who created the plugin, null when no creator is recorded. (see [below for nested schema](#nestedatt--created_by))
- `description` (String) The served version's manifest `description`, or null.
- `display_name` (String) The served version's manifest `displayName`, or null.
- `id` (String) The plugin's ID (prefixed `plugin_`).
- `latest_version_id` (String) The newest version (prefixed `pluginver_`), which differs from `served_version_id` while a stored version is not served.
- `manifest_version` (String) The served version's manifest `version`, normalized for display (a leading `v` dropped), or null.
- `name` (String) The manifest's `name`: a lowercase identifier, unique within the marketplace and fixed for the plugin's lifetime.
- `organization_installation_preference` (String) The organization-wide installation setting: the plugin's own, or its marketplace's default (`required`, `auto_install`, `available` or `not_available`).
- `organization_installation_preference_inherited` (Boolean) Whether `organization_installation_preference` comes from the marketplace's default.
- `reach` (String) How far the served version reaches: `remote` (declares an MCP server or a CLI), `privileged` (runs or takes effect on members' machines: hooks, settings, pre-approved tools) or `contained`; null when unclassified.
- `served_version_pinned` (Boolean) Whether the served version is pinned: `false` while it follows the newest version, `true` once a version was chosen (here or in claude.ai), for good.
- `updated_at` (String) RFC 3339 timestamp of the plugin's last served-version change or new version (installation settings do not move it).

<a id="nestedatt--components"></a>
### Nested Schema for `components`

Read-Only:

- `description` (String) What the component declares about itself; always null for MCP servers, hooks and CLIs.
- `name` (String) The component's name: an MCP server's key in the manifest, a hook's event, a CLI's executable.
- `type` (String) `skill`, `mcp_server`, `command`, `agent`, `hook` or `cli`.


<a id="nestedatt--content_scan"></a>
### Nested Schema for `content_scan`

Read-Only:

- `assessment` (String) Set once `status` is `completed`: `pass`, `warn`, `fail` or `unknown`.
- `reason` (String) For `warn` and `fail`, the main concern (for example `credential-exposure`), otherwise null.
- `status` (String) `processing` while the scan runs, `completed` when it finished, `errored` when it could not.


<a id="nestedatt--created_by"></a>
### Nested Schema for `created_by`

Read-Only:

- `api_key_id` (String) The Admin API key's ID, for an `api_actor`.
- `email_address` (String) The member's email address, for a `user_actor` (may be null).
- `type` (String) `user_actor` (a member in claude.ai) or `api_actor` (an Admin API key); other types may appear.
- `user_id` (String) The member's user ID, for a `user_actor`.

## Import

Import an existing organization-owned plugin using its ID. An imported plugin needs neither `source_dir` nor `source_archive`: they are only read to create one.

```shell
terraform import anthropic_plugin.example <plugin_id>
```
