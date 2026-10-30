---
page_title: "Provider: Anthropic"
description: |-
  Use the Anthropic Terraform provider to interact with Anthropic APIs.
---

# Anthropic Provider

The Anthropic provider is used to interact with [Anthropic](https://anthropic.com) APIs.
It allows you to manage and query Anthropic resources such as models.

## Authentication

The provider accepts three distinct credentials, depending on which resources you manage. At least one must be configured, unless a [cloud platform](#cloud-platforms) block is; they are independent, and configuring one never substitutes for another.

Anthropic issues several kinds of keys, and each endpoint accepts only some of them ([Admin API authentication](https://platform.claude.com/docs/en/manage-claude/admin-api#authentication), [Admin API keys](https://platform.claude.com/docs/en/manage-claude/admin-api-keys), [API key types](https://platform.claude.com/docs/en/manage-claude/authentication#key-types)). This table says which one to put in which provider argument:

| Provider argument | Key | Prefix | Created in | Use it for |
|---|---|---|---|---|
| `api_key` | Claude API key (personal, service account or legacy workspace key) **scoped to one workspace** | `sk-ant-api03-` | Claude Console, [API keys](https://platform.claude.com/settings/keys) | Every resource outside the organization APIs: messages, agents, environments, skills, files, vaults, memory stores, models, etc. |
| `admin_api_key` | Claude Console Admin API key | `sk-ant-admin01-` | Claude Console, [Admin keys](https://platform.claude.com/settings/admin-keys), by a member with the admin role | Workspaces and their members, workspace rate limits, API keys, the organization, organization members and invites |
| `admin_api_key` | Personal or service account key **not** scoped to a workspace | `sk-ant-api03-` | Claude Console, [API keys](https://platform.claude.com/settings/keys) | The same resources as a Console Admin API key, with the permissions of the user or service account the key belongs to |
| `admin_api_key` | Claude Enterprise Admin API key, with scopes | `sk-ant-api01-` | claude.ai, [Organization settings > API](https://claude.ai/admin-settings/api-access), by the primary owner | Organization members and invites only, according to its [scopes](https://platform.claude.com/docs/en/manage-claude/admin-api-keys#choose-scopes-for-a-claude-enterprise-key): `read:members` (or `read:org_audit`) to read them, `write:members` to change a member's role, remove a member, create or withdraw an invite |
| `auth_token`, or the `federation` block | OAuth bearer token with the `org:admin` scope | `sk-ant-oat01-` | `ant auth login`, or a Workload Identity Federation token exchange | Service accounts, federation issuers and federation rules, which accept no API key |

- `admin_api_key` is sent in the `x-api-key` header whichever kind of key it holds, so the provider needs no setting to tell them apart.
- Of the Admin API endpoints this provider calls, a Claude Enterprise organization can use only the member and invite ones; workspaces, API keys and rate limits are Claude Console only ([which endpoints each organization can use](https://platform.claude.com/docs/en/manage-claude/user-management#which-endpoints-can-your-organization-use)). Assigning the `managed` role, on an invite or a member, also needs the `write:rbac_groups` scope, which only a key created for all linked organizations can carry. A call outside the key's scopes returns `403` with the scopes the key has and the scopes the endpoint needs.
- A key manages only the organization it was created in. If your company uses both Claude Console and Claude Enterprise, create a key in each and configure one [provider alias](https://developer.hashicorp.com/terraform/language/providers/configuration#alias-multiple-provider-configurations) per organization.
- The Admin API accepts the `org:admin` OAuth token on its other endpoints too, but the provider sends `auth_token` only to the service account and federation endpoints: the other organization resources need `admin_api_key`.

### API Key (`api_key` / `ANTHROPIC_API_KEY`)

Required for all inference resources: `anthropic_message`, `anthropic_agent`, `anthropic_skill`, models, token counting, etc.

Generate one in the [Anthropic Console → API Keys](https://platform.claude.com/settings/keys). Scope it to the workspace the resources belong to: the provider sends no `anthropic-workspace-id` header, and the API rejects (`400`) a request from a key that is not scoped to a workspace when that header is missing ([Select a workspace](https://platform.claude.com/docs/en/manage-claude/authentication#select-a-workspace)).

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

Required for the organization resources and data sources built on the Admin API: workspaces and their members, workspace rate limits, API keys, the organization, organization members and invites. This is a separate credential scoped to your whole organization rather than a single workspace.

For a Claude Console organization, generate one in the [Claude Console → Admin keys](https://platform.claude.com/settings/admin-keys) page. For a Claude Enterprise organization, the primary owner creates a key with the scopes you need in [claude.ai → Organization settings → API](https://claude.ai/admin-settings/api-access); it reaches the organization members and invites only. A personal or service account key that is not scoped to a workspace is accepted too. See the table above.

**Environment variable** (recommended):

```bash
export ANTHROPIC_ADMIN_API_KEY="sk-ant-admin01-..."
```

**Provider argument**:

```hcl
provider "anthropic" {
  admin_api_key = "sk-ant-admin01-..."
}
```

The `admin_api_key` is optional — you only need it for the organization resources listed above.

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

~> **Warning**: These tokens are short-lived. A long `terraform apply` can outlive the token and start failing with `401`. Mint a fresh token immediately before the run; the provider cannot refresh a token it was handed. Use the `federation` block below when it needs to.

The `auth_token` is optional — you only need it for federation resources and MCP tunnels.

MCP tunnels (`anthropic_tunnel`, `anthropic_tunnel_certificate`, and the `anthropic_tunnel` / `anthropic_tunnels` data sources) reject API keys too, but need a bearer token with the `workspace:manage_tunnels` scope instead of `org:admin`. One provider block carries one bearer token, so give the tunnel resources their own aliased provider block; see [`anthropic_tunnel`](resources/tunnel).

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

Profiles under `~/.config/anthropic` are ignored: each client is built from the credential resolved above and nothing else, so a profile left active by `ant auth login` can never redirect a request to another base URL or scope it to another workspace behind your back.

~> **Warning**: Never hardcode API keys in your Terraform configuration files.
Use environment variables or a secrets manager instead.

## Cloud platforms

Anthropic also serves the Claude API through cloud gateways, each in front of an Anthropic organization of its own: an existing organization cannot be converted, and API keys, workspaces and Console settings do not carry over. A platform block makes a provider alias manage such an organization instead of one on the Claude API. Amazon Bedrock, Claude Models on Google Cloud (Google managed, on Vertex AI) and Microsoft Foundry serve the Messages API only, with no organization to manage, so this provider does not cover them.

~> **Beta.** Support for Claude Platform on AWS and Claude Models on Google Cloud is in beta: it is verified with offline tests only, against local fake gateways (requests signed and checked with the AWS SDK's own signer, Google tokens from local token endpoints, URL shapes, configuration rules), never against the real gateways, and every plan or apply prints a warning saying so for each alias with an `aws` or `google_cloud` block. Please [open an issue](https://github.com/ippontech/terraform-provider-anthropic/issues) to report what works and what does not.

The main points not verified yet, which the [Claude Platform on AWS](guides/claude_platform_on_aws#7-what-is-not-verified) and [Claude Models on Google Cloud](guides/claude_models_on_google_cloud#5-what-is-not-verified) guides list in full:

- whether the gateways serve the types marked Unverified in the [availability table](#availability-by-platform): scheduled deployments on AWS, user profiles on Google Cloud;
- the shape of the gateways' own errors, such as a 403 from IAM or a route a gateway does not serve;
- that `?beta=true`, which the SDK adds to beta routes, is accepted;
- on AWS, that the gateway canonicalizes the SigV4 signature of a path holding an encoded KMS key ARN as the AWS SDK signer does, and accepts an AWS Console key as `x-api-key`, as the SDK sends it, while the platform page documents `Authorization: Bearer`;
- on Google Cloud, that the request URLs the SDK derives (workspace and organization endpoints) are the gateway's, beyond the documentation's examples.

### Claude Platform on AWS (`aws`)

[Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws) serves the API through `https://aws-external-anthropic.<region>.api.aws`, authenticated by AWS:

```hcl
provider "anthropic" {
  alias        = "aws_us_west_2"
  workspace_id = "wrkspc_01..."

  aws = {
    region  = "us-west-2"
    profile = "platform-admin"
  }
}
```

- Setting the block selects the platform. `AWS_REGION`, `AWS_DEFAULT_REGION`, `ANTHROPIC_AWS_API_KEY`, `ANTHROPIC_AWS_WORKSPACE_ID` and `ANTHROPIC_AWS_BASE_URL` fill its attributes but never select it on their own: CI jobs export them for other tools.
- Requests are signed with AWS SigV4 (service `aws-external-anthropic`), with the credentials of `access_key`/`secret_key` (and `session_token` for temporary ones), of a named `profile`, or of the AWS default credential chain (environment, shared files, SSO, web identity, ECS, EC2 instance metadata), resolved when the provider is configured. Static credentials are not refreshed: for a long apply, prefer `profile` or the default chain. Instead of signing, `api_key` (or `ANTHROPIC_AWS_API_KEY`) sends a key generated in the AWS Console; its principal needs the `aws-external-anthropic:CallWithBearerToken` action.
- `workspace_id` is required: the gateway needs the `anthropic-workspace-id` header on every inference and resource request. It is not sent on Admin API requests, which do not require it. With the `aws` block it is read from `ANTHROPIC_AWS_WORKSPACE_ID`, never from `ANTHROPIC_WORKSPACE_ID`. An alias that only manages workspaces needs one too: any workspace of the same region.
- A workspace is bound to the region of the endpoint it was created through and is reachable there only, so use one provider alias per region and workspace.
- `aws` conflicts with `api_key`, `admin_api_key`, `auth_token`, `federation` and `google_cloud`. `ANTHROPIC_API_KEY`, `ANTHROPIC_ADMIN_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL` and `ANTHROPIC_WORKSPACE_ID` are not read: no first-party credential is valid on this platform.
- `base_url` (or `ANTHROPIC_AWS_BASE_URL`) replaces the regional endpoint, with an AWS PrivateLink endpoint for instance.
- Every AWS account must enable outbound web identity federation once (`aws iam enable-outbound-web-identity-federation`) before its first request; until then every request fails with `Outbound web identity federation is disabled for your account`.

### Claude Models on Google Cloud (`google_cloud`)

[Claude Models on Google Cloud (Anthropic managed)](https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/anthropic-managed) serves the API through `https://claude.googleapis.com/v1/projects/<project>/locations/global/...`, authenticated by Google Cloud:

```hcl
provider "anthropic" {
  alias        = "gc_prod"
  workspace_id = "wrkspc_01..."

  google_cloud = {
    project = "my-project"
  }
}
```

- Setting the block selects the platform. `ANTHROPIC_GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_PROJECT`, `ANTHROPIC_GOOGLE_CLOUD_LOCATION`, `ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID` and `ANTHROPIC_GOOGLE_CLOUD_BASE_URL` fill its attributes but never select it on their own.
- Requests carry a Google OAuth 2.0 access token with the `cloud-platform` scope, as `Authorization: Bearer`. It comes from `credentials` (the contents of a service account key, authorized user, external account or impersonated service account file, refreshed as needed), from `access_token` (not refreshed: about one hour), or else from Application Default Credentials (`GOOGLE_APPLICATION_CREDENTIALS`, `gcloud auth application-default login`, the metadata server of a workload on Google Cloud). The identity needs `roles/claude.user` on the project, or `roles/claude.admin` for the organization routes.
- `project` falls back to the variables above, then to the project of the credentials when they carry one. `location` defaults to `global`, which the documentation recommends.
- `workspace_id` goes into the request URL, never into a header (the gateway rejects `anthropic-workspace-id`). With the `google_cloud` block it is read from `ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID`, never from `ANTHROPIC_WORKSPACE_ID`. It may be left unset only for an alias limited to organization routes: then every type that calls workspace routes fails at plan time. Workspaces are created in the Claude Console (Settings > Workspaces), where their IDs are shown.
- `google_cloud` conflicts with `api_key`, `admin_api_key`, `auth_token`, `federation` and `aws`, and the first-party variables are not read.
- `base_url` (or `ANTHROPIC_GOOGLE_CLOUD_BASE_URL`) replaces both derived endpoints, so every route, organization routes included, goes to it.

The [Claude Platform on AWS](guides/claude_platform_on_aws) and [Claude Models on Google Cloud](guides/claude_models_on_google_cloud) guides cover the cloud-side setup: IAM permissions, roles, keys and credentials.

### Availability by platform

Every resource and data source works on the Claude API. On a cloud platform, a type the gateway does not serve fails at plan time with an error naming the platform, before any request; an unverified type is allowed. Types missing from this table are supported on every platform.

<!-- platform-availability:start -->
| Type | Kind | Claude Platform on AWS | Claude Models on Google Cloud (Anthropic managed) |
|---|---|---|---|
| `anthropic_api_key` | resource, data source | Unavailable | Unavailable |
| `anthropic_api_keys` | data source | Unavailable | Unavailable |
| `anthropic_claude_tag_bundle` | data source | Unverified | Unverified |
| `anthropic_claude_tag_bundle_target` | resource | Unverified | Unverified |
| `anthropic_claude_tag_bundles` | data source | Unverified | Unverified |
| `anthropic_claude_tag_slack_channel` | resource, data source | Unverified | Unverified |
| `anthropic_claude_tag_slack_workspace_settings` | resource | Unverified | Unverified |
| `anthropic_claude_tag_slack_workspaces` | data source | Unverified | Unverified |
| `anthropic_compliance_settings` | resource | Unverified | Unverified |
| `anthropic_deployment` | resource | Unverified | Supported |
| `anthropic_deployment_runs` | data source | Unverified | Supported |
| `anthropic_external_key` | resource, data source | Unverified | Unverified |
| `anthropic_external_keys` | data source | Unverified | Unverified |
| `anthropic_federation_issuer` | resource, data source | Unavailable | Unavailable |
| `anthropic_federation_issuers` | data source | Unavailable | Unavailable |
| `anthropic_federation_rule` | resource, data source | Unavailable | Unavailable |
| `anthropic_federation_rule_workspace` | resource | Unavailable | Unavailable |
| `anthropic_federation_rule_workspaces` | data source | Unavailable | Unavailable |
| `anthropic_federation_rules` | data source | Unavailable | Unavailable |
| `anthropic_invite` | resource, data source | Unavailable | Unavailable |
| `anthropic_invites` | data source | Unavailable | Unavailable |
| `anthropic_organization` | data source | Unavailable | Unavailable |
| `anthropic_organization_member` | resource, data source | Unavailable | Unavailable |
| `anthropic_organization_members` | data source | Unavailable | Unavailable |
| `anthropic_organization_webhook` | resource | Unverified | Unverified |
| `anthropic_plugin` | resource, data source | Unverified | Unverified |
| `anthropic_plugin_installation_setting` | resource | Unverified | Unverified |
| `anthropic_plugin_marketplace` | data source | Unverified | Unverified |
| `anthropic_plugin_marketplace_settings` | resource | Unverified | Unverified |
| `anthropic_plugin_marketplaces` | data source | Unverified | Unverified |
| `anthropic_plugin_version` | resource | Unverified | Unverified |
| `anthropic_plugin_versions` | data source | Unverified | Unverified |
| `anthropic_plugins` | data source | Unverified | Unverified |
| `anthropic_rbac_group` | resource, data source | Unverified | Unverified |
| `anthropic_rbac_group_member` | resource | Unverified | Unverified |
| `anthropic_rbac_groups` | data source | Unverified | Unverified |
| `anthropic_service_account` | resource, data source | Unavailable | Unavailable |
| `anthropic_service_account_workspace` | resource | Unavailable | Unavailable |
| `anthropic_service_account_workspaces` | data source | Unavailable | Unavailable |
| `anthropic_service_accounts` | data source | Unavailable | Unavailable |
| `anthropic_spend_limit` | resource | Unverified | Unverified |
| `anthropic_telemetry_destination` | resource | Unverified | Unverified |
| `anthropic_tunnel` | resource, data source | Unverified | Unverified |
| `anthropic_tunnel_certificate` | resource | Unverified | Unverified |
| `anthropic_tunnels` | data source | Unverified | Unverified |
| `anthropic_user_profile` | resource, data source | Supported | Unverified |
| `anthropic_user_profiles` | data source | Supported | Unverified |
| `anthropic_vault` | data source | Unverified | Unverified |
| `anthropic_vaults` | data source | Unverified | Unverified |
| `anthropic_workspace` | resource, data source | Supported | Unavailable |
| `anthropic_workspace_member` | resource, data source | Unavailable | Unavailable |
| `anthropic_workspace_members` | data source | Unavailable | Unavailable |
| `anthropic_workspace_rate_limit` | resource | Unverified | Unverified |
| `anthropic_workspace_rate_limits` | data source | Unavailable | Unavailable |
| `anthropic_workspaces` | data source | Supported | Unavailable |
<!-- platform-availability:end -->

On Claude Platform on AWS, the gateway serves only the workspace, external key and telemetry destination routes of the Admin API: AWS IAM manages organization and workspace membership, and API keys are generated in the AWS Console. It accepts no OAuth token, so Workload Identity Federation does not apply. Scheduled deployments are listed by the platform page but not by the gateway's route table, which denies unlisted routes, hence unverified; their agent also needs an `aws_role` execution identity.

On Claude Models on Google Cloud, the gateway serves the external key routes of the Admin API, but no workspace, member, invite, API key, rate limit or organization route: workspaces are managed in the Claude Console. Google Cloud IAM controls access, and there are no API keys or Anthropic OAuth tokens. User profiles are absent from the platform's documentation, hence unverified.

On a platform, a `404` counts as "deleted outside Terraform" only when its body is the Claude API's own `not_found_error`. Any other 404, such as a gateway refusing a route it does not serve, fails the operation and keeps the object in state.

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

- `admin_api_key` (String, Sensitive) The key for the Admin API's organization management endpoints (workspaces and their members, workspace rate limits, API keys, the organization, organization members and invites): a Claude Console Admin API key (`sk-ant-admin01-...`), a Claude Enterprise Admin API key with scopes (`sk-ant-api01-...`, members and invites only), or a personal or service account key not scoped to a workspace. Sent in the `x-api-key` header. Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.
- `api_key` (String, Sensitive) The Anthropic API key. Can also be set via the ANTHROPIC_API_KEY environment variable.
- `auth_token` (String, Sensitive) An OAuth bearer token for endpoints that reject API keys: an org:admin token (`sk-ant-oat01-...`) for the Workload Identity Federation admin endpoints, or a workspace:manage_tunnels token for MCP tunnels (configure each in its own, aliased provider block). Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.
- `aws` (Attributes) **Beta.** Reach Claude Platform on AWS instead of the Claude API: the provider alias then manages the Anthropic organization tied to an AWS account, through the aws-external-anthropic gateway of one AWS region. Setting the block is what selects the platform; the AWS and ANTHROPIC_AWS_* environment variables only fill its attributes. Conflicts with api_key, admin_api_key, auth_token, federation and google_cloud, and the ANTHROPIC_API_KEY, ANTHROPIC_ADMIN_API_KEY, ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL variables are ignored: no first-party credential is valid there. workspace_id is required. Requests are signed with AWS SigV4 (service aws-external-anthropic), with credentials from access_key/secret_key, profile or the AWS default chain, unless api_key is set. Resources the gateway does not serve fail at plan time. Support for this platform is verified with offline tests only, and the provider prints a warning saying so. (see [below for nested schema](#nestedatt--aws))
- `federation` (Attributes) Obtain the OAuth bearer token through Workload Identity Federation instead of `auth_token`. The federation rule's scope decides which endpoints the token can call: org:admin for the Workload Identity Federation admin endpoints, workspace:manage_tunnels for MCP tunnels. The provider exchanges an identity token from your platform (a CI OIDC token, a Kubernetes projected service account token, an Entra token...) for an Anthropic access token, and exchanges again before it expires, fetching a fresh identity token each time. Every attribute falls back to the environment variable the Anthropic SDKs read. Conflicts with `auth_token`; an exported ANTHROPIC_AUTH_TOKEN is ignored, with a warning. Give the federation rule a `token_lifetime_seconds` well above 120: the SDK refreshes within the last 120 seconds of a token's life, so a shorter lifetime refreshes, and fetches an identity token, on nearly every request. (see [below for nested schema](#nestedatt--federation))
- `google_cloud` (Attributes) **Beta.** Reach Claude Models on Google Cloud (Anthropic managed) instead of the Claude API: the provider alias then manages the Anthropic organization tied to a Google Cloud project, through claude.googleapis.com. Setting the block is what selects the platform; the ANTHROPIC_GOOGLE_CLOUD_* and GOOGLE_CLOUD_PROJECT environment variables only fill its attributes. Conflicts with api_key, admin_api_key, auth_token, federation and aws, and the first-party ANTHROPIC_* variables are ignored. Requests carry a Google access token (cloud-platform scope) from credentials, access_token or Application Default Credentials. workspace_id goes into the request URL, never into a header, and may be left unset only for an alias limited to organization routes. Resources the gateway does not serve fail at plan time. Support for this platform is verified with offline tests only, and the provider prints a warning saying so. (see [below for nested schema](#nestedatt--google_cloud))
- `workspace_id` (String) Workspace (`wrkspc_...`) the standard API key acts in, sent as the `anthropic-workspace-id` header on every request of the standard client. Required with a personal or service account key that is not scoped to a single workspace; must be left unset with a single-workspace key, which the API rejects with a 404 when the header names any workspace, its own included. Not sent on the Admin API or the org:admin OAuth client, which select their workspace otherwise. Has no environment fallback: ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` only. With the aws block: required, sent on every request except the Admin API ones, and read from ANTHROPIC_AWS_WORKSPACE_ID when unset. With the google_cloud block: part of the request URL, never a header, required except for an alias limited to organization routes, and read from ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID when unset.

<a id="nestedatt--aws"></a>
### Nested Schema for `aws`

Optional:

- `access_key` (String) AWS access key ID for SigV4. Requires secret_key; conflicts with profile and api_key.
- `api_key` (String, Sensitive) API key generated in the AWS Console (Claude Platform on AWS > API keys), sent instead of a SigV4 signature; the principal needs the aws-external-anthropic:CallWithBearerToken action. Not a Claude Console key. Conflicts with profile and access_key. Can also be set via the ANTHROPIC_AWS_API_KEY environment variable, which is only read when the block sets no credential.
- `base_url` (String) Gateway base URL, in place of the one derived from region (an AWS PrivateLink endpoint, for instance). Can also be set via the ANTHROPIC_AWS_BASE_URL environment variable; ANTHROPIC_BASE_URL is not read.
- `profile` (String) Named profile of the AWS shared configuration files to take credentials from (SSO, credential_process and role assumption included). Conflicts with access_key and api_key. Without profile, access_key or api_key, the AWS default credential chain applies.
- `region` (String) AWS region of the gateway endpoint (https://aws-external-anthropic.<region>.api.aws) and of the SigV4 signature. A workspace is bound to the region it was created in and is reachable only there, so use one provider alias per region. Can also be set via the AWS_REGION or AWS_DEFAULT_REGION environment variable.
- `secret_key` (String, Sensitive) AWS secret access key, with access_key.
- `session_token` (String, Sensitive) AWS session token of temporary credentials, with access_key and secret_key. Static credentials are not refreshed: for a long apply prefer profile or the default chain.


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


<a id="nestedatt--google_cloud"></a>
### Nested Schema for `google_cloud`

Optional:

- `access_token` (String, Sensitive) A Google OAuth 2.0 access token with the cloud-platform scope, for instance data.google_client_config.this.access_token. It is not refreshed and typically expires after one hour, so a longer apply fails: prefer credentials or Application Default Credentials. Conflicts with credentials.
- `base_url` (String) Base URL for every route, in place of the ones derived from project, location and workspace (the workspace endpoint, and the organization endpoint for /v1/organizations routes). Can also be set via the ANTHROPIC_GOOGLE_CLOUD_BASE_URL environment variable; ANTHROPIC_BASE_URL is not read.
- `credentials` (String, Sensitive) Contents of a Google credential file: a service account key, an authorized user, an external account (Workload Identity Federation) or an impersonated service account. Tokens are refreshed as they expire. Conflicts with access_token. Without either, Application Default Credentials apply (GOOGLE_APPLICATION_CREDENTIALS, gcloud's application default login, the metadata server of the workload).
- `location` (String) Gateway location. Defaults to `global`, which the documentation recommends; it does not pin where inference runs (use inference_geo for that). Can also be set via the ANTHROPIC_GOOGLE_CLOUD_LOCATION environment variable.
- `project` (String) Google Cloud project whose Anthropic organization the alias manages. Can also be set via the ANTHROPIC_GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT environment variable; otherwise the project of the credentials, when they carry one.
