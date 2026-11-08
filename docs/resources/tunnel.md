---
page_title: "anthropic_tunnel Resource - anthropic"
subcategory: "Tunnels"
description: |-
  Creates and manages an MCP tunnel (research preview).
---

# anthropic_tunnel (Resource)

Creates and manages an [MCP tunnel](https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview): an Anthropic-assigned domain through which Managed Agents sessions and the Messages API reach MCP servers inside your private network, over an outbound-only connection run by `cloudflared` and Anthropic's proxy. Each MCP server behind the proxy is reached at `https://<route>.<domain>/`.

~> **Research preview.** MCP tunnels require [access](https://claude.com/form/mcp-tunnels), are provided as-is, and the Tunnels API may change without a deprecation period. The organization can hold at most 10 active tunnels.

A new tunnel rejects MCP traffic until at least one CA certificate is registered on it with `anthropic_tunnel_certificate`.

## Lifecycle

- **Creation is not idempotent**: each create allocates a fresh, globally unique domain. The provider retries it only when the API answers `429` (rate limited), which means the request was not processed, so one resource never creates two tunnels; but a create that failed after reaching the API (a timeout, a dropped connection) can leave a tunnel Terraform does not track. Check the Console's MCP tunnels list before re-running such an apply, and import or archive what you find.
- **No update endpoint**: changing `display_name` replaces the tunnel, which allocates a new `domain` and `token`.
- **Destroy archives the tunnel, irreversibly.** There is no delete endpoint. Archiving also archives every certificate on the tunnel, retires its domain for good and invalidates its token.
- A tunnel **archived outside Terraform** (for example in response to a suspected compromise) can never carry traffic again: the next refresh drops it from state and the next apply creates a replacement, with a new domain.

## Token

`token` is the connector token `cloudflared` runs the tunnel with. The provider reveals it once, right after creation (or at the first apply after an import), and keeps it in the Terraform state, marked sensitive: protect the state like any other secret store, or set `store_token = false` (see below). A plan or a refresh never reveals it, because each reveal is recorded as a `tunnel_token_revealed` compliance event and every plan would otherwise add one.

To rotate the token, change `token_rotation` (any value, like the setup component's `--token-version`): the provider calls `rotate_token`, and `token` and `token_id` change in place. Established connections are not severed, but a restarted connector needs the new value. The first value you set, including on an existing or imported tunnel, is recorded without rotating. A rotation made outside Terraform is not detected; change `token_rotation` to bring the state back in line.

### Keeping the token out of the state

Set `store_token = false` when something other than Terraform hands the connector its token, for example a setup that fetches it with its own `workspace:manage_tunnels` credential, so that no tunnel secret reaches the state. The provider then never reveals the token, not after creation and not after an import: `token` and `token_id` stay null. Changing `token_rotation` still rotates the token, without storing the new value, so the connector must fetch the new token itself before it restarts.

```terraform
resource "anthropic_tunnel" "corp" {
  provider     = anthropic.tunnels
  display_name = "corp-mcp"
  store_token  = false
}
```

Switching an existing tunnel to `false` removes the token from the state at the next apply, with no API call. Switching back to `true` shows an in-place update, and the apply reveals the token and stores it.

## Authentication

The Tunnels API rejects API keys: it takes an OAuth bearer token with the `workspace:manage_tunnels` scope, minted through [Workload Identity Federation](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation) from a federation rule created in the Console (an OAuth caller, and so `anthropic_federation_rule`, can only create `workspace:developer` and `workspace:inference` rules). The tunnel is created in the workspace that token is scoped to. Since a provider block holds one bearer token, give the tunnel resources their own provider block:

```terraform
provider "anthropic" {
  alias = "tunnels"

  federation = {
    organization_id     = "00000000-0000-0000-0000-000000000000"
    federation_rule_id  = "fdrl_..." # a rule scoped to workspace:manage_tunnels
    service_account_id  = "svac_..."
    identity_token_file = "/var/run/secrets/anthropic.com/tunnels/token"
  }
}

resource "anthropic_tunnel" "corp" {
  provider     = anthropic.tunnels
  display_name = "corp-mcp"
}
```

When the default provider block is federated too, for the `org:admin` resources, give each block its own identity token source. Both blocks exchange a token in the same run, and the exchange accepts an identity token carrying a `jti` claim (GitHub Actions and Kubernetes projected tokens do) only once per issuer, so two blocks reading the same token file fail with `jti_reused`. Use a second projected token at another path, as above, or an `identity_token_command` that fetches a fresh token on every exchange.

> **API**: `POST /v1/tunnels`, `GET /v1/tunnels/{tunnel_id}`, `POST /v1/tunnels/{tunnel_id}/{archive,reveal_token,rotate_token}` (Tunnels API). The deprecated Admin API endpoints under `/v1/organizations/tunnels` are not used.
> **Auth**: OAuth bearer token with the `workspace:manage_tunnels` scope (`auth_token` or `federation`). API keys are not accepted.
> **Beta header**: `anthropic-beta: mcp-tunnels-2026-06-22`.
> **Cost**: none documented for the research preview.

## Example Usage

```hcl
resource "anthropic_tunnel" "corp" {
  display_name = "corp-mcp"

  # Change this value (to "2", a date, ...) to rotate the connector token.
  token_rotation = "1"
}

output "tunnel_domain" {
  description = "Domain of the tunnel: MCP servers behind it are reached at https://<route>.<domain>/."
  value       = anthropic_tunnel.corp.domain
}

output "tunnel_token" {
  description = "Connector token cloudflared runs the tunnel with."
  value       = anthropic_tunnel.corp.token
  sensitive   = true
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `display_name` (String) Human-readable name for the tunnel, 1-255 characters. The API cannot rename a tunnel, so changing this forces replacement: a new tunnel with a new `domain` and `token`, and the old one archived.
- `store_token` (Boolean) Whether the provider keeps the connector token in the Terraform state. Default: `true`. With `false`, the provider never reveals the token: `token` and `token_id` stay null, and a `token_rotation` change still rotates the token, without storing the new value. Use it when the connector fetches its token itself, so that no tunnel secret reaches the state. Switching to `false` removes the stored token from the state at the next apply; switching back to `true` reveals the token at the next apply.
- `token_rotation` (String) Arbitrary change-detection value, like the setup component's `--token-version`. Changing it from one non-null value to another rotates the tunnel token in place (`rotate_token`): the old token stops working for new connections, established ones are not severed, and a restarted connector must use the new `token`. The first value set (at creation, on import, or added to an existing tunnel) is recorded without rotating, and removing it does not rotate either.

### Read-Only

- `created_at` (String) Creation timestamp (RFC 3339).
- `domain` (String) Anthropic-assigned hostname for the tunnel. MCP server URLs whose host is a subdomain of this value (`<route>.<domain>`) are routed through the tunnel. Globally unique and never reused.
- `id` (String) Unique tunnel identifier assigned by the API (`tnl_...`).
- `token` (String, Sensitive) The connector token `cloudflared` runs the tunnel with. Treat it as a credential: it is stored in the Terraform state. Revealed once at creation, or by the first apply after an import, then kept from state; it changes only when `token_rotation` triggers a rotation. A rotation made outside Terraform is not detected. Null when `store_token` is `false`.
- `token_id` (String) Stable, non-secret identifier of the current token value. Changes when the token is rotated. Null when `store_token` is `false`.

## Import

Import an existing tunnel using its ID:

```shell
terraform import anthropic_tunnel.example <tunnel_id>
```

The import itself does not reveal the token, and neither does any plan: the plan after an import shows an in-place update of `token` and `token_id` (known after apply), and the first apply reveals the current token once and stores it in the state. With an `import` block, the same apply imports the tunnel and reveals its token. Setting `token_rotation` on an imported tunnel records it without rotating.
