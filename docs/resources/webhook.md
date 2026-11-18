---
page_title: "anthropic_webhook Resource - anthropic"
subcategory: "Managed Agents"
description: |-
  Experimental. Manages a workspace webhook endpoint that receives Managed Agents events (sessions, vaults, agents, deployments, environments, memory stores).
---

# anthropic_webhook (Resource)

~> **Experimental.** Anthropic documents [Managed Agents webhooks](https://platform.claude.com/docs/en/managed-agents/webhooks) as registered in the Claude Console only: its API reference does not describe the endpoint routes, and no SDK manages them. This resource calls the routes the Console uses, which the Claude Platform on AWS [IAM actions](https://platform.claude.com/docs/en/api/claude-platform-on-aws-iam-actions#webhooks) page lists. Their request and response shapes may change without notice, so this resource may have to change or be removed in a minor release rather than wait for the next major version.

Manages a workspace webhook endpoint: an HTTPS URL that receives an event when a Managed Agents session, vault, vault credential, agent, deployment, deployment run, environment or memory store changes. Each delivery carries the event `type` and the object `id`, not the object itself. The endpoint belongs to the workspace of the standard API key (or of the provider's `workspace_id`). It is distinct from `anthropic_organization_webhook`, which receives organization-level events through the Admin API.

> **API**: `POST /v1/webhooks`, `GET/POST/DELETE /v1/webhooks/{id}`, `POST /v1/webhooks/{id}/regenerate_signing_secret` (Managed Agents API, not in the API reference).
> **Auth**: Standard API key (`api_key` / `ANTHROPIC_API_KEY`), or the `aws` provider block on Claude Platform on AWS.
> **Beta header**: `anthropic-beta: managed-agents-2026-04-01`, sent by the provider on every request.
> **Cost**: Free.

## Availability

- **Claude Platform on AWS**: verified. The full lifecycle (create, update, disable, regenerate the secret, import, delete) was exercised through the gateway. The principal needs `CreateWebhook`, `GetWebhook`, `ListWebhooks`, `UpdateWebhook`, `DeleteWebhook` and `RotateWebhookSecret` on the workspace ARN; `RotateWebhookSecret` is not matched by the `Create*`, `Update*` or `Delete*` wildcards.
- **Claude API**: verified. The full lifecycle works with a standard API key; a key not scoped to one workspace needs the provider's `workspace_id`, which routes the endpoint to that workspace.
- **Claude Models on Google Cloud (Anthropic managed)**: unavailable. The webhooks page lists the Claude API and Claude Platform on AWS only.

An endpoint is removed from state only when its own read answers `404` while the list of endpoints still answers; if the list answers `404` too (the beta version is retired, say), the refresh fails and the endpoint stays in state with its signing secret. The routes are scoped to one workspace, so this does not cover a provider pointed at another workspace (a different `workspace_id` or key): there the list answers, and the endpoint is dropped from state as deleted, as other workspace resources are, while it still exists in its own workspace.

## Signing secret

Deliveries follow the [Standard Webhooks](https://www.standardwebhooks.com/) specification and are signed with the endpoint's `whsec_` signing secret. The API returns the secret only when the endpoint is created and when the secret is regenerated, never on a read, so the provider stores it in Terraform state, as `aws_iam_access_key` does for its secret, and keeps it there across refreshes. `signing_secret` is marked sensitive, but anyone who can read the state can read it: protect the state accordingly, and pass the secret to your receiver from it (for example into a secrets manager, where the SDKs' `unwrap()` helper reads it as `ANTHROPIC_WEBHOOK_SIGNING_KEY`).

Change `signing_secret_version` to rotate the secret: the next apply regenerates it and stores the new value. The old secret stops working at once, so update the receiver in the same apply. Removing `signing_secret_version` from the configuration does not rotate anything.

## Enabled state

`enabled` maps the endpoint's `state`. If Anthropic disables an endpoint on its own, `disabled_reason` says why, and the next apply turns the endpoint back on as long as `enabled` is `true`. Events that happen while an endpoint is disabled are never delivered.

The API ignores `state` on create, so every endpoint starts enabled: with `enabled = false`, the provider disables it in a second call right after the create, and deliveries for events in between may still be sent.

An enabled URL is unique across the whole organization, not only the workspace: creating or enabling an endpoint on a URL that another enabled endpoint uses answers `409` (`an enabled webhook endpoint with this url already exists in this organization`), even when that endpoint is in another workspace, and the error names neither. Disabling the other endpoint frees the URL.

`enabled_events` is checked by the API, not the provider: an unknown event type fails the apply with the list of accepted types, so event types added by Anthropic work without a provider release.

`name` and `description` are kept as they are when removed from the configuration.

## Example Usage

```hcl
resource "anthropic_webhook" "example" {
  url  = "https://example.com/anthropic-webhooks"
  name = "Session status"

  enabled_events = [
    "session.status_idled",
    "session.status_terminated",
  ]

  # Change this value to regenerate the signing secret. The old secret stops
  # working at once, so update the receiver in the same apply.
  signing_secret_version = 1
}

output "webhook_id" {
  description = "ID of the webhook endpoint."
  value       = anthropic_webhook.example.id
}

output "webhook_signing_secret" {
  description = "Secret to verify the Standard Webhooks signature of each delivery with."
  value       = anthropic_webhook.example.signing_secret
  sensitive   = true
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `enabled_events` (Set of String) The event types delivered to the endpoint, at least one (for example `session.status_idled`; see the [webhooks](https://platform.claude.com/docs/en/managed-agents/webhooks) page). The API validates them: an unknown type fails the apply with the list of accepted ones. An update replaces the endpoint's whole set.
- `url` (String) The HTTPS URL that deliveries are sent to: port 443, with a publicly resolvable hostname.

### Optional

- `description` (String) Notes about the endpoint. Removing it from the configuration keeps the current value.
- `enabled` (Boolean) Whether deliveries are sent to the endpoint (the API's `state`). Default: `true`. Anthropic can disable an endpoint on its own (see `disabled_reason`); the next apply then turns it back on. Events that happen while the endpoint is disabled are never delivered.
- `name` (String) A short label for the endpoint. Removing it from the configuration keeps the current value.
- `signing_secret_version` (Number) Rotation trigger for `signing_secret`. Changing it to a new value (setting it for the first time on an existing endpoint included) regenerates the secret on the next apply: the old secret stops working at once, so update the receiver right away.

### Read-Only

- `created_at` (String) RFC 3339 timestamp of when the endpoint was created.
- `disabled_reason` (String) Why Anthropic disabled the endpoint automatically, or null when it is enabled or was disabled through `enabled = false`.
- `id` (String) ID of the webhook endpoint (`wep_...`).
- `signing_secret` (String, Sensitive) The `whsec_`-prefixed secret Anthropic signs deliveries with ([Standard Webhooks](https://www.standardwebhooks.com/)). Only the create and regenerate responses carry it, so the provider keeps it in state from those calls. Null after `terraform import`: change `signing_secret_version` to regenerate it and capture the new value.
- `updated_at` (String) RFC 3339 timestamp of the last change to the endpoint.

## Import

Import a webhook endpoint using its ID:

```shell
terraform import anthropic_webhook.example wep_011CZkZDf8HnUq2mTxRbVn4s
```

No read returns the signing secret, so `signing_secret` is null after an import. To get one into state, set `signing_secret_version` (or change it) on the imported resource: the next apply regenerates the secret, which invalidates the previous one.
