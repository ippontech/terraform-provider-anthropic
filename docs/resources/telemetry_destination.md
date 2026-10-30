---
page_title: "anthropic_telemetry_destination Resource - anthropic"
subcategory: "Claude Console: Organization"
description: |-
  Manages an OpenTelemetry destination that receives the traces of the organization's Messages API requests and Managed Agents sessions, via the Admin API.
---

# anthropic_telemetry_destination (Resource)

Manages an [OpenTelemetry destination](https://platform.claude.com/docs/en/manage-claude/opentelemetry/api): an OTLP/HTTP endpoint of your observability platform to which the API exports a span for each model request and tool use of your Messages API requests and Managed Agents sessions, for the workspaces the destination covers. It is what **Add destination** does on the Console's OpenTelemetry settings page.

> **API**: `POST /v1/organizations/telemetry_destinations`, `GET/POST/DELETE /v1/organizations/telemetry_destinations/{id}`, `GET /v1/organizations/telemetry_destinations/{id}/workspaces` (Admin API).
> **Auth**: Admin API key (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`).
> **Beta header**: `telemetry-destinations-2026-08-11`, sent by the provider on every request.
> **Cost**: Free.

## Availability

OpenTelemetry export is in beta, for Claude Platform organizations. On Claude Platform on AWS, destinations are managed through the AWS endpoint with AWS credentials, which this provider does not call. An organization can have at most **10** destinations, disabled ones included. With a HIPAA, FERPA or K-12 data-handling restriction, destinations export metadata only, whatever their `content_mode`. Once delivered, spans are held by your observability platform under its own retention and access controls.

## Headers

`headers_wo` holds the request headers sent with every delivery, typically the collector's authentication header. It is a write-only argument (Terraform 1.11 or later): the API stores the values encrypted and never returns them, and Terraform never stores them in state or plan files. Pass them from an ephemeral variable or an ephemeral resource to keep them out of every artifact.

Terraform cannot compare a write-only value with what the API holds, so:

- `headers_wo_version` is required with `headers_wo`. Change it to send changed header values.
- The provider sends `headers_wo` again with every update of the destination, and always when `endpoint_url` changes hostname or port: the API then removes every registered header, and the request must carry the new ones.
- `header_names` reports the names the API has. A name added to or removed from `headers_wo`, or added or removed outside Terraform, shows up in the plan and the next apply sends `headers_wo` again; a header that is no longer configured is removed.
- Leaving `headers_wo` out of the configuration leaves the registered headers alone, which is useful after an import (a hostname or port change still removes them, and the plan warns about it). To remove every header, set `headers_wo = {}`.

## Workspaces

`workspaces` is sent whole whenever it changes, and the API replaces the destination's list with it, so the per-workspace `POST`/`DELETE …/workspaces/{workspace_id}` endpoints are not needed. A request carries at most 100 workspace IDs, which caps `workspace_ids` at 100. Reads return only the scope's `type`, so the provider lists `…/workspaces` to report `workspace_ids` and detect changes made outside Terraform.

## Delivery state

`enabled` maps the destination's state. The API disables a destination on its own after a `3xx` redirect, or when every delivery fails for 24 hours; `disabled_reason` then says why, and the next apply turns the destination back on as long as `enabled` is `true`. Spans produced while a destination is disabled are never delivered. `delivery_health` reports the most recent delivery results.

Sending a test span (`POST …/{id}/test`, or **Test** in the Console) is an action, not configuration, and is not part of this resource.

## Example Usage

```hcl
variable "collector_api_key" {
  description = "API key the observability platform's OTLP endpoint authenticates deliveries with."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "anthropic_telemetry_destination" "example" {
  display_name = "Acme observability platform"
  endpoint_url = "https://gateway.example.com/otlp"

  # Write-only: sent to the API, never stored in state. Change
  # headers_wo_version to send new values.
  headers_wo = {
    authorization = "Bearer ${var.collector_api_key}"
  }
  headers_wo_version = 1

  workspaces = {
    type          = "include"
    workspace_ids = ["wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"]
  }

  traces = {
    content_mode      = "content"
    max_content_bytes = 786432
  }
}

output "telemetry_destination_id" {
  description = "ID of the telemetry destination."
  value       = anthropic_telemetry_destination.example.id
}

output "telemetry_destination_header_names" {
  description = "Names of the headers registered on the destination (their values are never returned)."
  value       = anthropic_telemetry_destination.example.header_names
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `endpoint_url` (String) Base URL of the collector's OTLP/HTTP endpoint, for example `https://gateway.example.com/otlp`: `https`, a hostname rather than an IP address, port 443 if any, no query string, fragment or credentials, at most 2,048 characters. Leave out the per-signal path (`/v1/traces`), which the API appends. **Changing the hostname or port removes every registered header**: the provider re-sends `headers_wo` in the same request when it is set.
- `workspaces` (Attributes) Which of the organization's workspaces the destination covers. Sent whole on every change. (see [below for nested schema](#nestedatt--workspaces))

### Optional

> **NOTE**: [Write-only arguments](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments) are supported in Terraform 1.11 and later.

- `display_name` (String) Human-readable name, 1 to 255 characters. Removing it clears the name.
- `enabled` (Boolean) Whether the destination receives deliveries (the API's `state`). Default: `true`. The API disables a destination on its own after a redirect or 24 hours of failed deliveries (see `disabled_reason`); the next apply then turns it back on. Spans produced while it is disabled are never delivered.
- `headers_wo` (Map of String, Sensitive, [Write-only](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments)) Request headers sent with every delivery, typically the collector's authentication header. Write-only: the API stores the values encrypted and never returns them, and they never reach Terraform state. Requires `headers_wo_version`, and Terraform >= 1.11. At most 20 headers; names compare case-insensitively. Omit it to leave the registered headers alone; set it to `{}` to remove them all.
- `headers_wo_version` (Number) Rotation trigger for `headers_wo`. Terraform cannot diff write-only values, so change this value to re-send the header values. Required when `headers_wo` is set.
- `traces` (Attributes) Settings for the traces the destination receives. Omit the block to keep the API's values (`metadata_only`, enabled, no traces URL, no content bound on create). (see [below for nested schema](#nestedatt--traces))

### Read-Only

- `created_at` (String) RFC 3339 timestamp of when the destination was created.
- `delivery_health` (Attributes) The most recent delivery results. (see [below for nested schema](#nestedatt--delivery_health))
- `disabled_reason` (String) Why the destination is disabled: `caller` (through `enabled = false`), `sustained_failure`, `invalid_address` or `redirect`. Null while it is enabled.
- `header_names` (Set of String) Names of the registered headers, as the API returns them. A name added or removed outside Terraform shows up as a change, and the next apply re-sends `headers_wo`.
- `id` (String) ID of the destination (`tdst_...`).
- `updated_at` (String) RFC 3339 timestamp of the last change to the destination's configuration or state.

<a id="nestedatt--workspaces"></a>
### Nested Schema for `workspaces`

Required:

- `type` (String) `all` (every workspace, the default workspace and future ones included), `include` (only the listed ones) or `exclude` (every workspace but the listed ones).

Optional:

- `workspace_ids` (Set of String) IDs (`wrkspc_...`) of the workspaces on the list, for `include` and `exclude`; not allowed with `all`. At most 100, the API's cap per request. Omit it for an empty list: an `include` destination then covers no workspace, an `exclude` one covers them all.


<a id="nestedatt--traces"></a>
### Nested Schema for `traces`

Optional:

- `content_mode` (String) `metadata_only` (default: identifiers, timing, status, token counts) or `content` (adds prompts, responses and tool inputs and outputs). Organizations with a HIPAA, FERPA or K-12 data-handling restriction export metadata only.
- `enabled` (Boolean) Whether the destination receives traces. Default: `true`.
- `endpoint_url` (String) Full URL of the collector's traces endpoint, when it is not `endpoint_url` plus `/v1/traces`. Same scheme, host and port as `endpoint_url`; the API posts to it as given. Removing it goes back to `endpoint_url` plus `/v1/traces`, and so does a change of the hostname or port of `endpoint_url` while the traces block is omitted.
- `max_content_bytes` (Number) Most bytes of content one exported span can hold, applied once `content_mode` is `content`. Null means only the API's maximum applies. Removing it keeps the current value.


<a id="nestedatt--delivery_health"></a>
### Nested Schema for `delivery_health`

Read-Only:

- `last_error` (String) Message of the most recent failed delivery, for example `TLS failure` or `HTTP error (status 503)`; null if none failed. A later success does not clear it.
- `last_error_at` (String) RFC 3339 timestamp of the failure `last_error` describes.
- `last_success_at` (String) RFC 3339 timestamp of the most recent successful delivery.

## Import

Import a telemetry destination using its ID:

```shell
terraform import anthropic_telemetry_destination.example tdst_01AAAAAAAAAAAAAAAAAAAAAA
```

Header values cannot be read back. Leave `headers_wo` out of the configuration to keep the registered headers, or set it with `headers_wo_version` to replace them on the next apply.
