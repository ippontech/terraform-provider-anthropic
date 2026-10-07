---
page_title: "Provider: Anthropic"
description: |-
  Use the Anthropic Terraform provider to interact with Anthropic APIs.
---

# Anthropic Provider

The Anthropic provider is used to interact with [Anthropic](https://anthropic.com) APIs.
It allows you to manage and query Anthropic resources such as models.

## Authentication

The provider accepts three distinct credentials, depending on which resources you manage. At least one must be configured; they are independent, and configuring one never substitutes for another.

### API Key (`api_key` / `ANTHROPIC_API_KEY`)

Required for all inference resources: `anthropic_message`, `anthropic_agent`, `anthropic_skill`, models, token counting, etc.

Generate one in the [Anthropic Console → API Keys](https://platform.claude.com/settings/keys).

**Environment variable** (recommended):

```bash
export ANTHROPIC_API_KEY="sk-ant-api03-..."
```

**Provider argument**:

```hcl
provider "anthropic" {
  api_key = "sk-ant-api03-..."
}
```

### Workspace (`workspace_id`)

API keys are [personal or service account keys](https://platform.claude.com/docs/en/manage-claude/authentication#key-types), bound to an identity rather than to a workspace. A key created for a single workspace needs nothing more. A key that is not must say which workspace each request acts in, or the API answers `400 anthropic-workspace-id is required when authenticating with an identity-linked API key`. Set `workspace_id` and the provider sends that header on every request of the standard client, so one key serves one provider alias per workspace:

```hcl
provider "anthropic" {
  alias        = "prod"
  workspace_id = "wrkspc_01..."
}

provider "anthropic" {
  alias        = "staging"
  workspace_id = "wrkspc_02..."
}
```

Leave it unset with a single-workspace key: the API answers `404 Workspace not found` to any `anthropic-workspace-id` header on such a key, even one naming the key's own workspace. For the same reason `workspace_id` has no environment fallback: `ANTHROPIC_WORKSPACE_ID` is the Anthropic SDKs' federation variable, often exported for other tools in the same job, and it is read by `federation.workspace_id` only. The header goes on the standard client only: the Admin API is organization-wide, and a federated `org:admin` bearer selects its workspace at exchange time. An SDK profile's `workspace_id` is never read. A `workspace_id` unknown at plan time (a reference to a workspace created in the same run) is an error, as for the `federation` block: use a separate configuration or a known ID.

### Admin API Key (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`)

Required for organization-management resources: `anthropic_workspace`, workspace members, and workspace rate limits. This is a separate credential scoped to your whole organization rather than a single workspace.

Generate one in the [Anthropic Console → Admin API Keys](https://platform.claude.com/settings/admin-keys).

**Environment variable** (recommended):

```bash
export ANTHROPIC_ADMIN_API_KEY="sk-ant-admin03-..."
```

**Provider argument**:

```hcl
provider "anthropic" {
  admin_api_key = "sk-ant-admin03-..."
}
```

The `admin_api_key` is optional — you only need it when using workspace-related resources.

### OAuth Bearer Token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`)

Required for the endpoints that reject API keys outright and expect an `Authorization: Bearer` header carrying the `org:admin` scope — currently the [Workload Identity Federation admin endpoints](https://platform.claude.com/docs/en/manage-claude/wif-admin-api). An Admin API key is **not** accepted there, for reads or for writes.

Obtain a token with the `ant` CLI:

```bash
ant auth login --profile admin --scope "org:admin"
export ANTHROPIC_AUTH_TOKEN="$(ant auth print-credentials --profile admin --access-token)"
```

**Provider argument**:

```hcl
provider "anthropic" {
  auth_token = "sk-ant-oat01-..."
}
```

~> **Warning**: These tokens are short-lived. A long `terraform apply` can outlive the token and start failing with `401`. Mint a fresh token immediately before the run; the provider cannot refresh a token it was handed. Use the `federation` or `cli` block below when it needs to.

The `auth_token` is optional — you only need it for federation resources.

For the end-to-end setup (the one Console-only bootstrap rule, a complete GitHub Actions example, how the workload consumes the rule, importing wizard-created objects), see the [Workload Identity Federation guide](guides/workload_identity_federation).

### Workload Identity Federation (`federation`)

Instead of `auth_token`, the provider can obtain the `org:admin` bearer token itself, by exchanging an identity token from your platform through a federation rule whose OAuth scope is `org:admin`. It exchanges again shortly before the access token expires, fetching a fresh identity token each time, so long applies keep working and an identity token is never presented twice: the exchange accepts an assertion carrying a `jti` claim, as GitHub Actions tokens do, [only once per issuer](https://platform.claude.com/docs/en/manage-claude/wif-reference#jwt-verification).

```hcl
provider "anthropic" {
  federation = {
    organization_id    = "00000000-0000-0000-0000-000000000000"
    federation_rule_id = "fdrl_..."
    service_account_id = "svac_..."

    # One identity token source:
    identity_token_file = "/var/run/secrets/anthropic.com/token"
    # identity_token_command = ["az", "account", "get-access-token", "--resource", "<app-client-id>", "--query", "accessToken", "-o", "tsv"]
    # identity_token = "eyJ..."
  }
}
```

- `identity_token_file` is re-read on every exchange, for tokens a platform rotates on disk. The platform must have rotated the file by the next exchange: if it still holds a token with a `jti` the exchange has already accepted, that exchange fails with `jti_reused`.
- `identity_token_command` runs a program on every exchange and reads the token from its stdout. Use it for tokens fetched from a CLI or an endpoint. The program is run directly, not through a shell, so a pipe or `$VARIABLE` expansion needs the shell as the program: `["sh", "-c", "..."]`.
- `identity_token` is a literal and cannot be refreshed.

The command is stopped after 30 seconds. Its stderr is not shown in the error, since a failing CLI may echo the token there (`curl -v`, `az --debug`, `set -x`); it is logged at `TF_LOG=DEBUG`.

Give the rule a `token_lifetime_seconds` well above 120 (the default, 3600, is fine). The SDK refreshes within the last 120 seconds of an access token's life, so with a shorter lifetime every request refreshes, and runs the identity source, which can hit the identity provider's rate limit on a large plan.

Every attribute falls back to the environment variable the Anthropic SDKs read for their own federation (`ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_SERVICE_ACCOUNT_ID`, `ANTHROPIC_WORKSPACE_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`, `ANTHROPIC_IDENTITY_TOKEN`), so `federation = {}` with those exported is enough. The variables alone do nothing without the block: other tools in the same job often export them, and they must not change the provider's credential behind your back. `federation` conflicts with `auth_token`. An exported `ANTHROPIC_AUTH_TOKEN` is ignored, with a warning: the block wins, as configuration does over the environment everywhere else in the provider, although the Anthropic SDKs rank the variable above their own federation.

### `ant` CLI login (`cli`)

On an operator's workstation, the provider can use the `org:admin` login that the [`ant` CLI](https://platform.claude.com/docs/en/cli-sdks-libraries/cli/quickstart) stores, and refresh it with the profile's refresh token, so an apply never outlives the login:

```bash
ant auth login --profile admin --scope "org:admin"
```

```hcl
provider "anthropic" {
  cli = {
    profile = "admin"
    # Or, with no profile named, find the one org:admin login for:
    # organization_id = "00000000-0000-0000-0000-000000000000"
    # workspace_id    = "wrkspc_..."   # only to tell several apart
  }
}
```

The `cli` block is a fallback. It is used only when neither `auth_token` (or `ANTHROPIC_AUTH_TOKEN`) nor `federation` supplies the token, so one configuration can carry both: CI provides its token and never reads a profile, and an operator without one uses their own login.

- The profile is `profile`, else `ANTHROPIC_PROFILE`. With neither set, the provider looks among the `user_oauth` profiles with the `org:admin` scope for the one whose login is for `organization_id` (else `ANTHROPIC_ORGANIZATION_ID`) and, when set, `workspace_id` (else `ANTHROPIC_WORKSPACE_ID`). Finding none or several is an error.
- The profile must be logged in, and an expired login must be refreshable; otherwise the error names the `ant auth login` command to run.
- A profile whose `base_url` differs from the provider's (`ANTHROPIC_BASE_URL`, else production) is rejected. The profile's `workspace_id` is never sent: an `org:admin` token is organization-wide.

Without a `cli` block, profiles under `~/.config/anthropic` are ignored: each client is built from the credential resolved above and nothing else, so a profile left active by `ant auth login` can never redirect a request to another base URL or scope it to another workspace behind your back.

~> **Warning**: Never hardcode API keys in your Terraform configuration files.
Use environment variables or a secrets manager instead.

## Cost considerations

~> **Warning**: Some resources call billable Anthropic APIs at `terraform apply` time and can generate unbounded variable cost. `anthropic_message` consumes tokens on every apply, and Managed Agents / Skills (created via `anthropic_agent`, `anthropic_environment`, `anthropic_skill`, `anthropic_skill_version`) are free to manage but billable when invoked at runtime. Combining these with `count`/`for_each` over a large input set, or omitting `max_tokens`, can produce a single apply that costs significantly more than expected.

Each per-resource doc page lists its cost profile in an **API / Auth / Beta header / Cost** header block. Review it before scaling apply-time inference across many instances, and prefer setting an explicit `max_tokens` on every `anthropic_message`.

## Example Usage

```hcl
terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `admin_api_key` (String, Sensitive) The Anthropic Admin API key for organization management endpoints (workspaces, members). Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.
- `api_key` (String, Sensitive) The Anthropic API key. Can also be set via the ANTHROPIC_API_KEY environment variable.
- `auth_token` (String, Sensitive) An org:admin OAuth bearer token (`sk-ant-oat01-...`) for endpoints that reject API keys, such as the Workload Identity Federation admin endpoints. Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.
- `cli` (Attributes) Obtain the org:admin OAuth bearer token from a profile written by `ant auth login`, refreshing it with the profile's refresh token. Used only when neither `auth_token` nor `federation` supplies the token, so the same configuration runs locally as the operator and in CI as a workload. Without `profile`, the provider looks for the one org:admin profile that matches `organization_id` (and `workspace_id`, when set). (see [below for nested schema](#nestedatt--cli))
- `federation` (Attributes) Obtain the org:admin OAuth bearer token through Workload Identity Federation instead of `auth_token`. The provider exchanges an identity token from your platform (a CI OIDC token, a Kubernetes projected service account token, an Entra token...) for an Anthropic access token, and exchanges again before it expires, fetching a fresh identity token each time. Every attribute falls back to the environment variable the Anthropic SDKs read. Conflicts with `auth_token`; an exported ANTHROPIC_AUTH_TOKEN is ignored, with a warning. Give the federation rule a `token_lifetime_seconds` well above 120: the SDK refreshes within the last 120 seconds of a token's life, so a shorter lifetime refreshes, and fetches an identity token, on nearly every request. (see [below for nested schema](#nestedatt--federation))
- `workspace_id` (String) Workspace (`wrkspc_...`) the standard API key acts in, sent as the `anthropic-workspace-id` header on every request of the standard client. Required with a personal or service account key that is not scoped to a single workspace; must be left unset with a single-workspace key, which the API rejects with a 404 when the header names any workspace, its own included. Not sent on the Admin API or the org:admin OAuth client, which select their workspace otherwise. Has no environment fallback: ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` only.

<a id="nestedatt--cli"></a>
### Nested Schema for `cli`

Optional:

- `organization_id` (String) UUID of the Anthropic organization, used to find the profile when none is named. Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.
- `profile` (String) Name of the `ant` profile to use. Can also be set via the ANTHROPIC_PROFILE environment variable.
- `workspace_id` (String) Workspace (`wrkspc_...`) the profile was logged in to, used to narrow the search when none is named. Can also be set via the ANTHROPIC_WORKSPACE_ID environment variable.


<a id="nestedatt--federation"></a>
### Nested Schema for `federation`

Optional:

- `federation_rule_id` (String) Federation rule (`fdrl_...`) to exchange against. Can also be set via the ANTHROPIC_FEDERATION_RULE_ID environment variable.
- `identity_token` (String, Sensitive) A literal identity token (JWT). It cannot be refreshed, so applies that outlive it fail; prefer `identity_token_file` or `identity_token_command`. Can also be set via the ANTHROPIC_IDENTITY_TOKEN environment variable.
- `identity_token_command` (List of String) Command (program and arguments) that prints an identity token on stdout. The program is run directly, not through a shell: for a pipe or `$VARIABLE` expansion, make the shell the program (`["sh", "-c", "..."]`). It runs on every exchange, so short-lived tokens, and tokens with a `jti` claim that the exchange accepts only once, are fetched fresh each time. It is stopped after 30 seconds. Its stderr is not shown in diagnostics, since a failing CLI may echo the token there; it is logged at TF_LOG=DEBUG.
- `identity_token_file` (String) Path to a file holding the identity token, re-read on every exchange so a rotating file (such as a Kubernetes projected token) stays current. The file must have been rotated by the next exchange if its token carries a `jti`, which the exchange accepts only once. Can also be set via the ANTHROPIC_IDENTITY_TOKEN_FILE environment variable.
- `organization_id` (String) UUID of the Anthropic organization. Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.
- `service_account_id` (String) Service account (`svac_...`) the rule targets; the exchange fails if the rule targets another. Can also be set via the ANTHROPIC_SERVICE_ACCOUNT_ID environment variable.
- `workspace_id` (String) Workspace (`wrkspc_...`) to scope the token to at exchange time. Required only when the rule is enabled for more than one workspace. Can also be set via the ANTHROPIC_WORKSPACE_ID environment variable, which the SDKs' own federation reads the same way.
