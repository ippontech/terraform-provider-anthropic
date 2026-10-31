---
page_title: "Claude Platform on AWS"
subcategory: "Guides"
description: |-
  Manage the Anthropic organization of an AWS account through Claude Platform on AWS: provider aliases per region and workspace, the IAM permissions of the Terraform principal, execution roles for scheduled deployments, and customer-managed keys in AWS KMS. Beta: verified offline only.
---

# Claude Platform on AWS

[Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws) is the Claude API operated by Anthropic and reached through an AWS gateway, `https://aws-external-anthropic.<region>.api.aws`. AWS authenticates every request (SigV4 or a key generated in the AWS Console), IAM authorizes it, and AWS Marketplace bills it. Signing up creates a new Anthropic organization tied to the AWS account: an existing organization cannot be converted, and nothing in it carries over.

The provider reaches that organization through its `aws` block, documented with its rules in the "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs). This guide covers what surrounds it: the AWS-side setup, how to split provider aliases, the IAM permissions Terraform needs, execution roles for scheduled deployments, and customer-managed keys.

~> **Beta.** The provider's Claude Platform on AWS support is in beta, and every plan or apply prints a warning saying so. No Claude Platform on AWS organization was available to test against: the support is verified with offline tests only, against a local fake gateway that checks every request's SigV4 signature with the AWS SDK's own signer, never with requests to the real gateway. The main points not verified are whether the gateway serves scheduled deployments, the shape of its own errors, the signature of a path holding an encoded KMS key ARN, `?beta=true` on beta routes, and AWS Console keys sent as `x-api-key`; [section 7](#7-what-is-not-verified) lists them all. Please [open an issue](https://github.com/ippontech/terraform-provider-anthropic/issues) to report what works and what does not.

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

A workspace is bound to the AWS region of the endpoint it was created through and is reachable only there. Every inference and resource request names its workspace in the `anthropic-workspace-id` header, so a provider alias serves one region and one workspace. Admin API requests (workspaces) do not carry the header, but the alias still needs a workspace ID: give the one created at sign-up.

A workspace created in the same run cannot be the `workspace_id` of an alias: provider configuration must be known at plan time, and the provider rejects an unknown `workspace_id`. Manage workspaces from one alias, and their contents from aliases whose workspace IDs are known: a variable, as below, or a separate configuration that reads the first one's outputs.

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

The gateway maps every route to an action of the `aws-external-anthropic` namespace ([IAM actions](https://platform.claude.com/docs/en/api/claude-platform-on-aws-iam-actions)). Workspace creation and listing, user profiles and encryption key registrations are account-scoped and need `"Resource": "*"`; everything inside a workspace can be scoped to the workspace ARN, `arn:aws:aws-external-anthropic:<region>:<account-id>:workspace/<workspace-id>`. The managed policy `AnthropicFullAccess` grants everything; the policy below grants what this provider's resources call.

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
        ]
        Resource = "arn:aws:aws-external-anthropic:us-west-2:${data.aws_caller_identity.current.account_id}:workspace/${var.support_workspace_id}"
      },
    ]
  })
}
```

With `aws.api_key` instead of SigV4, the principal also needs `aws-external-anthropic:CallWithBearerToken` on `"Resource": "*"`. Mind the IAM wildcard pitfalls the IAM page describes: `Delete*` does not match `Archive*` actions, and `*File` also matches the user profile actions.

## 4. Execution role for scheduled deployments

On this platform a scheduled deployment runs each session as an IAM role of your AWS account ([scheduled deployments](https://platform.claude.com/docs/en/managed-agents/scheduled-deployments)): the agent's execution identity must be an `aws_role` before a deployment is created for it, or the create returns a 400.

~> `anthropic_deployment` and `anthropic_deployment_runs` are **unverified** on this platform: the platform page and the 2026-10-14 release notes announce scheduled deployments, but the gateway's route table, which denies the routes it does not list, does not list `/v1/deployments`. `anthropic_agent` does not manage the execution identity yet, so set it outside Terraform.

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

On this platform a workspace's `external_key_id` is the ARN of an AWS KMS key ([CMEK on Claude Platform on AWS](https://platform.claude.com/docs/en/manage-claude/cmek-claude-platform-on-aws)): a symmetric, single-Region key in the same account and region as the workspace, whose key policy grants the `aws-external-anthropic.amazonaws.com` service principal `kms:Encrypt`, `kms:Decrypt`, `kms:GenerateDataKey` and `kms:DescribeKey`. The key must be registered with Anthropic before it is attached: in the Claude Console (Data retention, Encryption keys) or through the external key endpoints, which this provider does not manage yet. There is no validation step: attaching performs an encrypt and a decrypt, so a key policy problem fails that create or update. Attaching is permanent: the key can be neither detached nor replaced.

The documented key policy can restrict the key to the workspaces it lists, through their `compartment_id`, which the workspace only gets once created. Referencing `anthropic_workspace.x.compartment_id` from the key policy while the workspace's `external_key_id` references the key is a dependency cycle, which Terraform refuses. Break it with variables, in three applies of the same configuration:

1. Apply with neither variable set: the workspace is created without a key, and its `compartment_id` is output.
2. Apply with `compartment_id` set to that output: the key is created with the restricted policy. Register its ARN with Anthropic.
3. Apply with `attach_key = true`: the key is attached, in place.

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
  external_key_id = var.attach_key ? aws_kms_key.regulated[0].arn : null
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
}

output "regulated_compartment_id" {
  description = "Value for var.compartment_id in the second apply."
  value       = anthropic_workspace.regulated.compartment_id
}
```

Without the compartment condition (a policy scoped with `aws:SourceArn` to `arn:aws:aws-external-anthropic:*:<account-id>:workspace/*`, for instance) there is no cycle: the key can exist and be registered before the workspace, and be attached at creation. To attach the key to another workspace later, add that workspace's compartment ID to the condition first.

The principal that attaches the key needs `kms:DescribeKey`, `kms:Encrypt` and `kms:Decrypt` on it, besides `CreateWorkspace` or `UpdateWorkspace`; registering it needs `aws-external-anthropic:RegisterKey`.

## 6. What the provider manages on this platform

- **Supported**: workspaces (create, rename, archive; `data_residency.workspace_geo` is not configurable here, leave it unset), agents, environments, vaults and their credentials, memory stores and memories, files, skills and skill versions, messages and token counting, models, user profiles (create, read, update; the provider never deletes them).
- **Unverified**: scheduled deployments and their runs ([section 4](#4-execution-role-for-scheduled-deployments)).
- **Unavailable**: API keys (generated in the AWS Console), organization members, invites and workspace members (AWS IAM manages access), rate limits, the organization data source, and Workload Identity Federation (the gateway accepts no OAuth token).
- **Not in the provider yet**: external key registrations and telemetry destinations, which the gateway serves.

Managed Agents sessions differ on this platform, which matters for agents and environments managed here: a session runs at most 6 hours without a user event before it needs one, and a session on a self-hosted environment cannot attach memory stores. The `computer_toolset_20260801` and `browser_toolset_20260801` toolsets and fast mode are not available.

## 7. What is not verified

Each of these needs a Claude Platform on AWS test organization:

1. Whether the gateway serves `/v1/deployments` and `/v1/deployment_runs`.
2. The shape of the gateway's own errors (403 from IAM, a route it does not serve). The provider keeps an object in state on any 404 that is not the Claude API's `not_found_error`, which is the safe side, but the message cannot be tuned without seeing one.
3. That the gateway canonicalizes the signature of a path holding an encoded KMS key ARN (`%2F`) as the AWS SDK signer does.
4. That `?beta=true`, which the SDK adds to beta routes, is accepted.
5. That an AWS Console key is accepted as `x-api-key`, as SDK v1.84 sends it; the current platform page documents `Authorization: Bearer`.
6. What the gateway does with `data_residency.workspace_geo` and `tags` on a workspace create, and whether the workspace object shows its region.
7. That Admin API requests without an `anthropic-workspace-id` header are accepted, as the platform page says.
8. What `aws_iam_outbound_web_identity_federation` does on destroy, and whether its `issuer_identifier` is the issuer URL a first-party federation issuer would register.

## See also

- The "Cloud platforms" section of the [provider page](https://registry.terraform.io/providers/ippontech/anthropic/latest/docs), with the availability table.
- [Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws) and its [IAM actions](https://platform.claude.com/docs/en/api/claude-platform-on-aws-iam-actions).
