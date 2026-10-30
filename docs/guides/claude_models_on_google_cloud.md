---
page_title: "Claude Models on Google Cloud"
subcategory: "Guides"
description: |-
  Manage the Anthropic organization of a Google Cloud project through Claude Models on Google Cloud (Anthropic managed): project services and IAM roles, Application Default Credentials, provider aliases, and what the provider can and cannot manage there. Beta: verified offline only.
---

# Claude Models on Google Cloud (Anthropic managed)

[Claude Models on Google Cloud (Anthropic managed)](https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/anthropic-managed) is the Claude API operated by Anthropic and reached through `https://claude.googleapis.com`, in your Google Cloud project. Google Cloud authenticates every request with a Google access token, IAM authorizes it on the project, and Google Cloud Marketplace bills it. Setup creates a new Anthropic organization per project: an existing organization cannot be converted, and nothing in it carries over.

This is not Claude Models on Google Cloud (Google managed), the Vertex AI offering, which serves the Messages API only and has no organization for this provider to manage.

The provider reaches the organization through its `google_cloud` block, documented with its rules in the "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs). This guide covers what surrounds it.

~> **Beta.** The provider's Claude Models on Google Cloud support is in beta, and every plan or apply prints a warning saying so. No Claude Models on Google Cloud organization was available to test against: the support is verified with offline tests only, against local fake gateways and token endpoints, never with requests to the real gateway. The main points not verified are the shape of the gateway's own errors, `?beta=true` on beta routes, whether user profiles, the Compliance API and Managed Agents webhooks are served, and the request URLs the SDK derives; [section 5](#5-what-is-not-verified) lists them all. Please [open an issue](https://github.com/ippontech/terraform-provider-anthropic/issues) to report what works and what does not. The offering launched on 2026-10-22, and its documentation still changes.

## 1. Before the first apply

1. Subscribe to the Claude Models on Google Cloud (Anthropic managed) listing in Google Cloud Marketplace.
2. Enable two services on the project: `claude.googleapis.com`, which serves the API and defines its IAM permissions, and `anthropic-managed-claude.cloudpartnerservices.goog`, which attributes the usage to your subscription. The second one can only be enabled by the account that completed the Marketplace purchase, according to the setup troubleshooting notes, so enable it by hand if Terraform's identity cannot.
3. On the project's Claude page of the Google Cloud console, click **Activate** and complete the Anthropic organization setup.
4. Open the Claude Console (only `roles/claude.admin` can) and note the workspace IDs under **Settings > Workspaces**. Setup creates a Default Workspace; more are created there too, never through the API.

The services and the roles of the identity that runs Terraform can be managed with the `hashicorp/google` provider. Do not remove the role grants of the service agent that enabling `claude.googleapis.com` creates: the platform stops working in the project without them.

```hcl
variable "project" {
  type        = string
  description = "Google Cloud project holding the Anthropic organization."
}

variable "terraform_service_account" {
  type        = string
  description = "Email of the service account that runs Terraform."
}

resource "google_project_service" "claude" {
  project            = var.project
  service            = "claude.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "claude_marketplace" {
  project            = var.project
  service            = "anthropic-managed-claude.cloudpartnerservices.goog"
  disable_on_destroy = false
}

# roles/claude.user covers every workspace resource; roles/claude.admin adds
# the organization routes and Console access.
resource "google_project_iam_member" "terraform_claude" {
  project = var.project
  role    = "roles/claude.user"
  member  = "serviceAccount:${var.terraform_service_account}"
}
```

## 2. Credentials

Each request carries a Google OAuth 2.0 access token with the `cloud-platform` scope. The provider takes it, in order, from:

- `google_cloud.credentials`, the contents of a credential file: a service account key, an authorized user, an external account (Workload Identity Federation for Google Cloud) or an impersonated service account. Tokens are refreshed as they expire.
- `google_cloud.access_token`, a token obtained elsewhere. It is not refreshed and usually expires after an hour, so a longer apply fails.
- Otherwise, Application Default Credentials: `GOOGLE_APPLICATION_CREDENTIALS`, the file `gcloud auth application-default login` writes on a workstation, or the service account attached to a workload on Google Cloud. Prefer this: rotation is Google's problem, and a CI job that authenticates to Google Cloud through Workload Identity Federation (`google-github-actions/auth` on GitHub, for instance) sets `GOOGLE_APPLICATION_CREDENTIALS` for you.

The project comes from `google_cloud.project`, then `ANTHROPIC_GOOGLE_CLOUD_PROJECT` or `GOOGLE_CLOUD_PROJECT`, then the credentials when they carry one. No Anthropic API key or OAuth token is accepted on this platform, and the provider ignores the first-party variables (`ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_WORKSPACE_ID` and the others) when the block is set.

## 3. Provider aliases

The workspace is part of every request's URL (`.../projects/<project>/locations/global/workspaces/<workspace>/invoke/v1/...`), never a header, so an alias serves one project and one workspace. Since workspaces are created in the Console, their IDs are always known inputs.

```hcl
variable "support_workspace_id" {
  type        = string
  description = "ID of the support workspace, from the Claude Console."
}

provider "anthropic" {
  alias        = "gc_support"
  workspace_id = var.support_workspace_id

  google_cloud = {
    project = var.project
    # credentials = file("terraform-sa.json")
  }
}

resource "anthropic_environment" "support" {
  provider = anthropic.gc_support
  name     = "support"
}

resource "anthropic_agent" "triage" {
  provider = anthropic.gc_support
  name     = "Support triage"
  model    = "claude-opus-5-5"
}
```

Organization routes (`/v1/organizations/...`) go to the organization endpoint (`.../claudeOrganization/invoke`) and need no workspace: an alias without `workspace_id` serves those types only, and refuses every other type at plan time. On the provider's current resources, every type that works on this platform needs a workspace.

## 4. What the provider can and cannot manage

- **Supported**: agents, environments, vaults and their credentials, memory stores and memories, files, skills and skill versions, scheduled deployments and their runs, messages and token counting, models.
- **Unverified**: user profiles. The platform's documentation does not mention them, and the SDK version this provider uses sends them to the workspace endpoint, while later versions send them to the organization endpoint.
- **Unavailable**: workspaces (Console only), API keys, organization members, invites and workspace members (Google Cloud IAM controls access, with roles granted on the project for every workspace at once), rate limits, the organization data source, and Workload Identity Federation (no Anthropic OAuth token).
- **Not in the provider yet**: external key registrations (Google Cloud KMS keys, attached to a workspace in the Console), which the gateway serves with `roles/claude.admin`.

A session on a self-hosted environment cannot attach memory stores, and the SDKs' pre-built self-hosted workers do not support this platform.

## 5. What is not verified

Each of these needs a Claude Models on Google Cloud test organization:

1. The shape of the gateway's own errors: a 403 from IAM, a route it does not serve. The provider keeps an object in state on any 404 that is not the Claude API's `not_found_error`.
2. That `?beta=true`, which the SDK adds to beta routes, is accepted.
3. Whether user profiles, the Compliance API and Managed Agents webhooks are served, and through which endpoint.
4. That the request URLs the SDK derives (workspace and organization endpoints, location `global`) are the gateway's, beyond the documentation's examples.

## See also

- The "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs), with the availability table.
- [Claude Models on Google Cloud (Anthropic managed)](https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/anthropic-managed) and its [IAM permissions](https://platform.claude.com/docs/en/api/claude-models-on-google-cloud-anthropic-managed-iam-permissions).
