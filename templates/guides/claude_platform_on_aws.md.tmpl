---
page_title: "Claude Platform on AWS"
subcategory: "Guides"
description: |-
  Manage the Anthropic organization of an AWS account through Claude Platform on AWS: provider aliases per region and workspace, the IAM permissions of the Terraform principal, execution roles for scheduled deployments, and customer-managed keys in AWS KMS. Beta.
---

# Claude Platform on AWS

[Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws) is the Claude API operated by Anthropic and reached through an AWS gateway, `https://aws-external-anthropic.<region>.api.aws`. AWS authenticates every request (SigV4 or an API key), IAM authorizes it, and AWS Marketplace bills it. Signing up creates a new Anthropic organization tied to the AWS account: an existing organization cannot be converted, and nothing in it carries over.

The provider reaches that organization through its `aws` block, documented with its rules in the "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs). This guide covers what surrounds it: the AWS-side setup, how to split provider aliases, the IAM permissions Terraform needs, execution roles for scheduled deployments, and customer-managed keys.

~> **Experimental** (see [Stability](../#stability)). Claude Platform on AWS support is experimental, and every plan or apply prints a warning saying so: it relies on the Anthropic SDK's AWS client, which Anthropic labels beta, so the `aws` block may change in a minor release, and its verification against the real gateway is still partial. [Section 7](#7-what-is-not-verified) lists what remains open. Please [open an issue](https://github.com/ippontech/terraform-provider-anthropic/issues) to report what works and what does not.

## 1. Before the first apply

1. Sign up from the Claude Platform on AWS page of the AWS Console, complete the Anthropic organization setup, and create the first workspace when the AWS Console prompts for it. Note its `wrkspc_...` ID: every provider alias needs a workspace ID, so the first one cannot come from Terraform.
2. Enable outbound web identity federation, once per AWS account. The gateway calls `sts:GetWebIdentityToken` on your behalf, and until this is enabled every request fails with `Outbound web identity federation is disabled for your account`:

   ```shell
   aws iam enable-outbound-web-identity-federation
   ```

   The `hashicorp/aws` provider (v6.70.0 at least) can manage the setting too, with a resource that takes no argument. What destroying it does, presumably disabling the feature for the whole account and every gateway request with it, was not checked, so protect it:

   ```hcl
   resource "aws_iam_outbound_web_identity_federation" "this" {
     lifecycle {
       prevent_destroy = true
     }
   }
   ```

3. Give the principal that runs Terraform the permissions of [section 3](#3-iam-permissions-of-the-terraform-principal).

## 2. One provider alias per region and workspace

A workspace is bound to the AWS region of the endpoint it was created through and is reachable only there. Every inference and resource request names its workspace in the `anthropic-workspace-id` header, so a provider configuration serves one region and one workspace. Admin API requests (workspaces) do not carry the header, but the configuration still needs a workspace ID: give the one created at sign-up. The workspace object does not show its region, so keep it next to the workspace ID, in the alias that reaches it.

Any provider configuration can carry the `aws` block, the default one included; the examples below use aliases because a configuration usually manages several regions or workspaces next to a first-party organization.

A workspace created in the same run cannot be the `workspace_id` of a provider configuration: provider configuration must be known at plan time, and the provider rejects an unknown `workspace_id`. Manage workspaces from one alias, and their contents from aliases whose workspace IDs are known: a variable, as below, or a separate configuration that reads the first one's outputs.

```hcl
variable "signup_workspace_id" {
  type        = string
  description = "Workspace created with the organization, in us-west-2."
}

variable "support_workspace_id" {
  type        = string
  description = "ID of anthropic_workspace.support, known after its first apply."
}

# Manages the workspaces of us-west-2.
provider "anthropic" {
  alias        = "usw2_admin"
  workspace_id = var.signup_workspace_id

  aws = {
    region = "us-west-2"
    # Credentials: the AWS default chain, or profile / access_key.
  }
}

resource "anthropic_workspace" "support" {
  provider = anthropic.usw2_admin
  name     = "support"
}

# Manages what lives in the support workspace.
provider "anthropic" {
  alias        = "usw2_support"
  workspace_id = var.support_workspace_id

  aws = {
    region = "us-west-2"
  }
}

resource "anthropic_agent" "triage" {
  provider = anthropic.usw2_support
  name     = "Support triage"
  model    = "claude-sonnet-5-5"
}
```

A resource the gateway does not serve (`anthropic_invite`, `anthropic_api_key`, the Workload Identity Federation resources, among others) fails at plan time with an error naming the platform; the "Availability by platform" table of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs) lists them.

## 3. IAM permissions of the Terraform principal

The gateway maps every route to an action of the `aws-external-anthropic` namespace ([IAM actions](https://platform.claude.com/docs/en/api/claude-platform-on-aws-iam-actions)). Workspace creation and listing, user profiles, telemetry destinations and encryption key registrations are account-scoped and need `"Resource": "*"`; everything inside a workspace can be scoped to the workspace ARN, `arn:aws:aws-external-anthropic:<region>:<account-id>:workspace/<workspace-id>`. The managed policy `AnthropicFullAccess` grants everything. The policy below grants the actions that page maps to the routes this provider's resources call. On that page a sub-resource route maps to its parent's actions: skill versions to `GetSkill` and `UpdateSkill`, vault credentials to `GetVault` and `UpdateVault`, memories to `GetMemoryStore` and `UpdateMemoryStore`. Scheduled deployments are the exception: the gateway serves their routes, but that page lists no action for them and which one the gateway checks is not verified, so the policy does not cover `anthropic_deployment` and `anthropic_deployment_runs`. A principal that manages them needs a broader grant, such as `AnthropicFullAccess` (`aws-external-anthropic:*`).

```hcl
data "aws_caller_identity" "current" {}

resource "aws_iam_policy" "terraform_anthropic" {
  name = "terraform-claude-platform"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "Workspaces"
        Effect = "Allow"
        Action = [
          "aws-external-anthropic:CreateWorkspace",
          "aws-external-anthropic:GetWorkspace",
          "aws-external-anthropic:ListWorkspaces",
          "aws-external-anthropic:UpdateWorkspace",
          "aws-external-anthropic:ArchiveWorkspace",
          "aws-external-anthropic:TagResource",
          "aws-external-anthropic:UntagResource",
          "aws-external-anthropic:ListTagsForResource",
        ]
        Resource = "*"
      },
      {
        Sid    = "UserProfiles"
        Effect = "Allow"
        Action = [
          "aws-external-anthropic:CreateUserProfile",
          "aws-external-anthropic:GetUserProfile",
          "aws-external-anthropic:ListUserProfiles",
          "aws-external-anthropic:UpdateUserProfile",
        ]
        Resource = "*"
      },
      {
        Sid    = "TelemetryDestinations"
        Effect = "Allow"
        Action = [
          "aws-external-anthropic:CreateAccountTelemetryDestination",
          "aws-external-anthropic:GetAccountTelemetryDestination",
          "aws-external-anthropic:UpdateAccountTelemetryDestination",
          "aws-external-anthropic:DeleteAccountTelemetryDestination",
          "aws-external-anthropic:ListAccountTelemetryDestinationWorkspaces",
        ]
        Resource = "*"
      },
      {
        Sid    = "ExternalKeys"
        Effect = "Allow"
        Action = [
          "aws-external-anthropic:RegisterKey",
          "aws-external-anthropic:GetKey",
          "aws-external-anthropic:ListKeys",
          "aws-external-anthropic:UpdateKey",
          "aws-external-anthropic:DisableKey",
        ]
        Resource = "*"
      },
      {
        Sid    = "SupportWorkspaceContents"
        Effect = "Allow"
        Action = [
          "aws-external-anthropic:CreateInference",
          "aws-external-anthropic:CountTokens",
          "aws-external-anthropic:GetModel",
          "aws-external-anthropic:ListModels",
          "aws-external-anthropic:CreateFile",
          "aws-external-anthropic:GetFile",
          "aws-external-anthropic:ListFiles",
          "aws-external-anthropic:DeleteFile",
          "aws-external-anthropic:CreateSkill",
          "aws-external-anthropic:GetSkill",
          "aws-external-anthropic:ListSkills",
          "aws-external-anthropic:UpdateSkill",
          "aws-external-anthropic:DeleteSkill",
          "aws-external-anthropic:CreateAgent",
          "aws-external-anthropic:GetAgent",
          "aws-external-anthropic:ListAgents",
          "aws-external-anthropic:UpdateAgent",
          "aws-external-anthropic:ArchiveAgent",
          "aws-external-anthropic:CreateEnvironment",
          "aws-external-anthropic:GetEnvironment",
          "aws-external-anthropic:ListEnvironments",
          "aws-external-anthropic:UpdateEnvironment",
          "aws-external-anthropic:ArchiveEnvironment",
          "aws-external-anthropic:DeleteEnvironment",
          "aws-external-anthropic:CreateVault",
          "aws-external-anthropic:GetVault",
          "aws-external-anthropic:ListVaults",
          "aws-external-anthropic:UpdateVault",
          "aws-external-anthropic:ArchiveVault",
          "aws-external-anthropic:DeleteVault",
          "aws-external-anthropic:CreateMemoryStore",
          "aws-external-anthropic:GetMemoryStore",
          "aws-external-anthropic:ListMemoryStores",
          "aws-external-anthropic:UpdateMemoryStore",
          "aws-external-anthropic:ArchiveMemoryStore",
          "aws-external-anthropic:DeleteMemoryStore",
          "aws-external-anthropic:CreateWebhook",
          "aws-external-anthropic:GetWebhook",
          "aws-external-anthropic:ListWebhooks",
          "aws-external-anthropic:UpdateWebhook",
          "aws-external-anthropic:DeleteWebhook",
          "aws-external-anthropic:RotateWebhookSecret",
        ]
        Resource = "arn:aws:aws-external-anthropic:us-west-2:${data.aws_caller_identity.current.account_id}:workspace/${var.support_workspace_id}"
      },
    ]
  })
}
```

Workspace tags are AWS resource tags: they work with IAM conditions (`aws:ResourceTag/<key>`) and cost allocation ([workspaces](https://docs.aws.amazon.com/claude-platform/latest/userguide/workspaces.html)). Changing them is authorized by `TagResource` and `UntagResource`, not `UpdateWorkspace`, which authorizes a `name` or `data_residency` change; tags set at creation need only `CreateWorkspace`. The provider sends a tags change in a request of its own, since the gateway rejects tags combined with any other field, and that request carries every configured key, unchanged ones included: a key left out would keep a value changed outside Terraform between plan and apply, and the apply would fail as an inconsistent result. So a change that removes a tag while other tags remain needs both `TagResource` and `UntagResource`, a change that only adds or changes keys needs `TagResource`, and `tags = {}` needs only `UntagResource`. Every workspace read also records `ListTagsForResource` in CloudTrail, so the policy grants it, although a policy without it was not tried.

With `aws.api_key` instead of SigV4, the principal also needs `aws-external-anthropic:CallWithBearerToken` on `"Resource": "*"`. With a short-term key from AWS's token generator, CloudTrail logs each call under the IAM identity that minted the key, with the same event names as signed calls, so the policy above applies unchanged. Mind the IAM wildcard pitfalls the IAM page describes: `Delete*` does not match `Archive*` actions, `RotateWebhookSecret` is matched by none of `Create*`, `Update*` and `Delete*`, and `*File` also matches the user profile actions.

## 4. Execution role for scheduled deployments

On this platform a scheduled deployment runs each session as an IAM role of your AWS account ([scheduled deployments](https://platform.claude.com/docs/en/managed-agents/scheduled-deployments)): the agent's execution identity must be an `aws_role` before a deployment is created for it, or the create returns a 400.

-> The gateway serves `/v1/deployments` and `/v1/deployment_runs`, although its route table does not list them. For an agent without an `aws_role` execution identity, the create fails with `On Claude Platform on AWS a deployment runs as the agent's AWS role. This agent has no execution_identity of type aws_role, so the deployment could never run.` Set it in the agent's `execution_identity` (see `anthropic_agent`).

```hcl
resource "aws_iam_role" "claude_sessions" {
  name = "claude-deployment-sessions"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "aws-external-anthropic.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:SetSourceIdentity"]
    }]
  })
}

resource "aws_iam_role_policy" "claude_sessions" {
  name = "create-sessions"
  role = aws_iam_role.claude_sessions.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "aws-external-anthropic:CreateSession",
        "sts:GetWebIdentityToken",
        "sts:TagGetWebIdentityToken",
      ]
      Resource = "*"
    }]
  })
}

# Whoever sets the role on the agent needs iam:PassRole on it; no managed
# policy grants it. The role must be in the same account.
resource "aws_iam_policy" "pass_claude_sessions_role" {
  name = "pass-claude-deployment-sessions"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "iam:PassRole"
      Resource = aws_iam_role.claude_sessions.arn
    }]
  })
}
```

## 5. Customer-managed keys in AWS KMS

On this platform a workspace's `external_key_id` is the ARN of an AWS KMS key ([CMEK on Claude Platform on AWS](https://platform.claude.com/docs/en/manage-claude/cmek-claude-platform-on-aws)): a symmetric, single-Region key in the same account and region as the workspace, whose key policy grants the `aws-external-anthropic.amazonaws.com` service principal `kms:Encrypt`, `kms:Decrypt`, `kms:GenerateDataKey` and `kms:DescribeKey`. The key must be registered with Anthropic before it is attached: in the Claude Console (Data retention, Encryption keys) or with `anthropic_external_key`, whose `id` is then the key's ARN. There is no validation step: attaching performs an encrypt and a decrypt, so a key policy problem fails that create or update. Attaching is permanent: the key can be neither detached nor replaced.

The documented key policy can restrict the key to the workspaces it lists, through their `compartment_id`, which the workspace only gets once created. Referencing `anthropic_workspace.x.compartment_id` from the key policy while the workspace's `external_key_id` references the key is a dependency cycle, which Terraform refuses. Break it with variables, in three applies of the same configuration:

1. Apply with neither variable set: the workspace is created without a key, and its `compartment_id` is output.
2. Apply with `compartment_id` set to that output: the key is created with the restricted policy and registered with Anthropic.
3. Apply with `attach_key = true`: the key is attached, in place.

Keep both values set after the third apply, in a `terraform.tfvars` file next to the configuration rather than on the command line. Without `compartment_id`, the key's `count` drops to zero and the plan would destroy the key the workspace's data is encrypted with, while the workspace keeps it attached. `prevent_destroy` on the key makes such a plan fail instead.

```hcl
variable "compartment_id" {
  type        = string
  default     = null
  description = "compartment_id output of the first apply."
}

variable "attach_key" {
  type        = bool
  default     = false
  description = "Attach the key, once it is registered with Anthropic. Permanent."
}

resource "anthropic_workspace" "regulated" {
  provider        = anthropic.usw2_admin
  name            = "regulated"
  external_key_id = var.attach_key ? anthropic_external_key.regulated[0].id : null
}

# The registration. Its id is the key ARN on this platform.
resource "anthropic_external_key" "regulated" {
  count    = var.compartment_id == null ? 0 : 1
  provider = anthropic.usw2_admin

  provider_config = {
    type    = "aws"
    kms_arn = aws_kms_key.regulated[0].arn
  }
}

resource "aws_kms_key" "regulated" {
  count = var.compartment_id == null ? 0 : 1

  description              = "Anthropic CMEK for the regulated workspace"
  key_usage                = "ENCRYPT_DECRYPT"
  customer_master_key_spec = "SYMMETRIC_DEFAULT"
  multi_region             = false

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AccountRootAdmin"
        Effect    = "Allow"
        Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
        Action    = "kms:*"
        Resource  = "*"
      },
      {
        Sid       = "AllowClaudePlatformOnAWSCrypto"
        Effect    = "Allow"
        Principal = { Service = "aws-external-anthropic.amazonaws.com" }
        Action    = ["kms:Encrypt", "kms:Decrypt", "kms:GenerateDataKey"]
        Resource  = "*"
        Condition = {
          StringEquals = { "kms:EncryptionContext:anthropic:compartment_uuid" = [var.compartment_id] }
        }
      },
      {
        Sid       = "AllowClaudePlatformOnAWSDescribe"
        Effect    = "Allow"
        Principal = { Service = "aws-external-anthropic.amazonaws.com" }
        Action    = "kms:DescribeKey"
        Resource  = "*"
      },
    ]
  })

  # Attached for good: a plan that would destroy the key fails instead.
  lifecycle {
    prevent_destroy = true
  }
}

output "regulated_compartment_id" {
  description = "Value for var.compartment_id in the second apply."
  value       = anthropic_workspace.regulated.compartment_id
}
```

Without the compartment condition (a policy scoped with `aws:SourceArn` to `arn:aws:aws-external-anthropic:*:<account-id>:workspace/*`, for instance) there is no cycle: the key can exist and be registered before the workspace, and be attached at creation. To attach the key to another workspace later, add that workspace's compartment ID to the condition first.

The principal that attaches the key needs `kms:DescribeKey`, `kms:Encrypt` and `kms:Decrypt` on it, besides `CreateWorkspace` or `UpdateWorkspace`; registering it needs `aws-external-anthropic:RegisterKey`, and deleting the registration `aws-external-anthropic:DisableKey`, which the gateway rejects while a workspace uses the key. Neither action is matched by a `Create*` or `Delete*` wildcard.

## 6. What the provider manages on this platform

- **Supported**: workspaces (create, rename, tag, archive; `data_residency.workspace_geo` takes `us`, the only workspace geo, or no value, which gives `us`), agents, environments, vaults and their credentials, memory stores and memories, files, skills and skill versions, messages and token counting, models, telemetry destinations, external key registrations (`anthropic_external_key`, whose `id` is the KMS key ARN, and its data sources), user profiles (create, read, update; the provider never deletes them), scheduled deployments and their runs ([section 4](#4-execution-role-for-scheduled-deployments)), workspace webhook endpoints (`anthropic_webhook`, experimental).
- **Unavailable**: API keys (generated in the AWS Console), organization members, invites and workspace members (AWS IAM manages access), rate limits, organization webhooks, the organization data source, Workload Identity Federation and MCP tunnels (the gateway accepts no OAuth token).

Managed Agents sessions differ on this platform, which matters for agents and environments managed here: a session runs at most 6 hours without a user event before it needs one, and a session on a self-hosted environment cannot attach memory stores. The `computer_toolset_20260801` and `browser_toolset_20260801` toolsets and fast mode are not available.

## 7. What is not verified

Verified against the real gateway:

- the deployment routes are served;
- a route the gateway does not serve answers 404 with an empty body, and an IAM deny answers 403 with a `permission_error` body naming the denied action;
- the signature of a path holding an encoded KMS key ARN, and `?beta=true` on beta routes, are accepted;
- Admin API requests need no `anthropic-workspace-id` header;
- API key authentication (`aws.api_key` and `ANTHROPIC_AWS_API_KEY`) with short-term keys from AWS's token generator: the gateway accepts the key as `x-api-key`, which the provider sends, and as `Authorization: Bearer`, which the platform page documents; CloudTrail logs each call under the IAM identity that minted the key, with the same event names as with SigV4, so the same IAM permissions apply;
- `workspace_geo` accepts `us` or no value; tags merge as on the Claude API, but only in a request of their own, which CloudTrail records as `TagResource` and `UntagResource` as [section 3](#3-iam-permissions-of-the-terraform-principal) describes; a `name` or `data_residency` update is recorded as `UpdateWorkspace`, and one that changes no tag sends no tags request;
- telemetry destinations (`anthropic_telemetry_destination`): a disabled destination scoped to one workspace, with a write-only header, is created, renamed, has its `headers_wo_version` raised and is destroyed, with an empty plan after each step; `header_names`, `disabled_reason` and `traces` come back as configured. The gateway rejects an `endpoint_url` whose host is not under a public top-level domain (`hostname must be under a public top-level domain`).
- workspace webhook endpoints (`anthropic_webhook`): an endpoint is created, renamed, has its events changed, is disabled and enabled again, has its signing secret regenerated through `signing_secret_version`, is imported (the next apply regenerates the secret, since `signing_secret_version` was set) and is destroyed, with an empty plan after each step, including a regeneration combined with a rename in one apply; a deleted endpoint answers 404 while the list still answers, so import of a deleted ID fails as expected; CloudTrail records the calls as `CreateWebhook`, `GetWebhook`, `UpdateWebhook` and `RotateWebhookSecret`.

Not verified yet:

1. Long-term API keys generated in the AWS Console: the gateway has rejected them with `401 authentication_error` (`The security token included in the request is invalid.`), in either header and with plain `curl` as well, and the cause is under investigation with AWS Support.
2. Attaching a KMS key to a workspace: the signature of the path is accepted, but no key was attached.
3. Whether reading a workspace fails without `ListTagsForResource`, which CloudTrail records on every read.
4. What `aws_iam_outbound_web_identity_federation` does on destroy, and whether its `issuer_identifier` is the issuer URL a first-party federation issuer would register.
5. Which IAM action authorizes the deployment routes, which the IAM actions page does not list ([section 3](#3-iam-permissions-of-the-terraform-principal)).
6. Registering, updating and deleting a key with `anthropic_external_key`: the IAM actions page lists the routes, but the provider has not called them on this platform.

## See also

- The "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs), with the availability table.
- [Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws) and its [IAM actions](https://platform.claude.com/docs/en/api/claude-platform-on-aws-iam-actions).
