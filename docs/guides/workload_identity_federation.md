---
page_title: "Bootstrap Workload Identity Federation with Terraform"
subcategory: "Workload Identity Federation"
description: |-
  End-to-end walkthrough: obtain an org:admin token, create the one Console-only rule, then manage issuers, service accounts and workspace-scoped rules with this provider so CI workloads call the Anthropic API without a long-lived key.
---

# Bootstrap Workload Identity Federation with Terraform

[Workload Identity Federation](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation) (WIF) lets a workload such as a GitHub Actions job exchange the short-lived OIDC token its platform already gives it for a short-lived Anthropic access token, so no Anthropic API key is stored in CI. Three objects make that work:

| Object | Resource | What it is |
|---|---|---|
| Federation issuer (`fdis_...`) | `anthropic_federation_issuer` | An OIDC identity provider your organization trusts, and how its signing keys are fetched. |
| Service account (`svac_...`) | `anthropic_service_account` | The non-human identity a minted token acts as. It carries no authorization on its own; workspace memberships (`anthropic_service_account_workspace`) say where it may act. |
| Federation rule (`fdrl_...`) | `anthropic_federation_rule` | Binds an issuer to a service account: tokens from the issuer whose claims match the rule mint access tokens for the service account, with a given OAuth scope, in the workspaces the rule is enabled for (`anthropic_federation_rule_workspace` adds more). |

The per-resource pages document each object. This guide covers the setup they exist for: the credential the provider needs, the one step the API cannot do for itself, a complete configuration, how the workload consumes it, and the operational rules that differ from most Terraform resources.

## 1. The credential: an `org:admin` OAuth bearer token

The WIF endpoints reject API keys, including Admin API keys, for reads as well as writes. The provider therefore needs its third credential, `auth_token` (or `ANTHROPIC_AUTH_TOKEN`): an OAuth bearer token carrying the `org:admin` scope. The scope is only granted to organization members with the admin, owner or primary owner role, and it applies to the whole organization regardless of workspace.

Obtain one interactively with the [`ant` CLI](https://platform.claude.com/docs/en/cli-sdks-libraries/cli/quickstart), under a profile reserved for administration:

```bash
ant auth login --profile admin --scope "org:admin"
export ANTHROPIC_AUTH_TOKEN="$(ant auth print-credentials --profile admin --access-token)"
```

```hcl
provider "anthropic" {
  # Reads ANTHROPIC_AUTH_TOKEN when unset. Keep the other credentials for
  # the resources that need them; they are independent.
  # auth_token = "sk-ant-oat01-..."
}
```

Three things to know about this token:

- **It is short-lived.** A long `terraform apply` can outlive it and start failing with `401`. Re-run the `export` line immediately before every run (the CLI refreshes the token on export); the provider does not refresh it.
- **The provider does not perform the WIF token exchange itself.** The SDK federation variables (`ANTHROPIC_FEDERATION_RULE_ID` and friends, see [section 4](#4-how-the-workload-consumes-the-rule)) do not configure the provider: with no `auth_token`, configuration fails with `Missing Credentials`. In CI, exchange the identity token in a preceding step and export the resulting bearer token; see [Bootstrap a workload to manage WIF](https://platform.claude.com/docs/en/manage-claude/wif-admin-api#bootstrap-a-workload-to-manage-wif).
- **`ant auth login --profile admin` also makes that profile active for the CLI.** The provider ignores profiles (every client is built from the resolved credential and nothing else), but the `ant` CLI and SDKs in the same shell do not. Switch back with `ant profile activate default` and unset the variable when you are done.

## 2. The once-per-organization bootstrap

An OAuth caller can only create or modify federation rules whose `oauth_scope` is `workspace:developer` or `workspace:inference`. The scope that lets a workload manage WIF itself, `org:admin`, can only be granted from the Claude Console: giving automation organization-admin access is a deliberate human action, so the API refuses to bootstrap it.

The consequence for Terraform is a single manual step per organization:

1. In the Console, go to **Settings → Workload identity → Connect workload** and create one federation rule for your infrastructure workload (for example the GitHub Actions workflow of the repository holding this Terraform configuration). Under **Advanced rule options**, set the OAuth scope to `org:admin`. The wizard creates the target service account with the Admin organization role, or lets you pick an existing admin service account.
2. Everything else, including issuers and every workspace-scoped rule, can now be created from Terraform, either by a human running `terraform apply` with the token from section 1, or by that bootstrapped workload once it exchanges its identity token.

~> **Warning**: Match the bootstrap rule to one exact workload identity, never a broad pattern. `subject_prefix` is an exact match unless the value ends in `*`. For GitHub Actions, pin it to a protected branch such as `repo:my-org/my-repo:ref:refs/heads/main`. A trailing wildcard such as `repo:my-org/my-repo:*` also matches `pull_request` runs, including runs from forks, so anyone able to open a pull request could mint an `org:admin` token.

One consequence to plan for: an OAuth caller cannot update an issuer that backs a rule with a scope other than `workspace:developer` or `workspace:inference`, and an organization can hold only **one issuer per `issuer_url`**. With GitHub Actions there is a single issuer URL, so the bootstrap rule and every workspace-scoped rule necessarily share the same `github-actions` issuer, and that issuer becomes read-only for Terraform: `terraform plan` still tracks it (import it, see [section 5](#5-importing-console-created-objects)), but any change to its `jwks`, `max_jwt_lifetime_seconds` or `check_jti` must be made in the Console. Only a bootstrap workload on a different identity provider (with its own issuer URL) avoids this.

Two things are possible from Terraform even for the Console-created objects: they can be [imported](#5-importing-console-created-objects) into state and read (a rule with `oauth_scope = "org:admin"` is readable through `anthropic_federation_rule`, it just cannot be created or changed by the provider), and a service account with `organization_role = "admin"` can be declared, but setting that role requires an interactive credential (a user token or a Console session), so a workload-minted token cannot create or promote one.

## 3. A complete example: GitHub Actions to a workspace

The configuration below trusts GitHub Actions, creates a developer service account, makes it a member of a production workspace, lets the `main` branch of one repository act as it there, and optionally enables the same rule for a staging workspace.

```hcl
terraform {
  required_version = ">= 1.11"
  required_providers {
    anthropic = {
      source  = "ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {
  # ANTHROPIC_AUTH_TOKEN, see section 1.
}

variable "production_workspace_id" {
  type = string
}

variable "staging_workspace_id" {
  type    = string
  default = null
}

# 1. Trust GitHub Actions' OIDC issuer. The issuer URL is publicly reachable
#    over HTTPS, so the default "discovery" mode fetches GitHub's signing keys
#    from its /.well-known/openid-configuration document. GitHub tokens are
#    short-lived: cap the accepted iat->exp spread accordingly instead of the
#    API default of 1h.
resource "anthropic_federation_issuer" "github_actions" {
  name       = "github-actions"
  issuer_url = "https://token.actions.githubusercontent.com"

  jwks = {
    type = "discovery"
  }

  max_jwt_lifetime_seconds = 600
}

# 2. The identity minted tokens act as. "developer" is the default and the
#    right role for an inference or deploy workload.
resource "anthropic_service_account" "gha_deploy" {
  name              = "gha-deploy"
  description       = "GitHub Actions deploy workflow of my-org/my-repo"
  organization_role = "developer"
}

# 3. A service account can only act in a workspace it is a member of. Every
#    service account is implicitly a member of the organization's default
#    workspace; any other workspace needs an explicit membership.
resource "anthropic_service_account_workspace" "gha_deploy_production" {
  service_account_id = anthropic_service_account.gha_deploy.id
  workspace_id       = var.production_workspace_id
  workspace_role     = "workspace_developer"
}

# 4. Bind the issuer to the service account. Only workflow tokens whose `sub`
#    claim is exactly the main branch of this repository qualify; the extra
#    claim match guards against a renamed or transferred repository.
resource "anthropic_federation_rule" "gha_deploy" {
  name        = "gha-deploy"
  description = "GitHub Actions deploy workflow on main"
  issuer_id   = anthropic_federation_issuer.github_actions.id

  match = {
    subject_prefix = "repo:my-org/my-repo:ref:refs/heads/main"
    claims = {
      repository_owner = "my-org"
    }
  }

  target = {
    service_account_id = anthropic_service_account.gha_deploy.id
  }

  # One of the two scopes an OAuth caller may grant.
  oauth_scope = "workspace:developer"

  # Enables the rule in this workspace at creation.
  workspace_id = var.production_workspace_id

  # Minted access tokens live at most this long (capped further by the
  # remaining validity of the identity token).
  token_lifetime_seconds = 900

  # The membership must exist before a minted token can act there.
  depends_on = [anthropic_service_account_workspace.gha_deploy_production]
}

# 5. Optional: enable the same rule for a second workspace. The service
#    account needs a membership there too.
resource "anthropic_service_account_workspace" "gha_deploy_staging" {
  count = var.staging_workspace_id == null ? 0 : 1

  service_account_id = anthropic_service_account.gha_deploy.id
  workspace_id       = var.staging_workspace_id
  workspace_role     = "workspace_developer"
}

resource "anthropic_federation_rule_workspace" "gha_deploy_staging" {
  count = var.staging_workspace_id == null ? 0 : 1

  federation_rule_id = anthropic_federation_rule.gha_deploy.id
  workspace_id       = var.staging_workspace_id

  depends_on = [anthropic_service_account_workspace.gha_deploy_staging]
}

# The workload needs these three values (section 4).
output "federation_rule_id" {
  value = anthropic_federation_rule.gha_deploy.id
}

output "service_account_id" {
  value = anthropic_service_account.gha_deploy.id
}

output "organization_id" {
  value = data.anthropic_organization.current.id
}

data "anthropic_organization" "current" {}
```

`data.anthropic_organization` needs the Admin API key (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`); drop it and read the organization ID from the Console if you do not want a second credential in this configuration. The token exchange ignores workspace membership for `org:admin` rules only; for the workspace-scoped rule above, both the membership and the rule enablement must exist.

## 4. How the workload consumes the rule

The workload never calls the exchange endpoint by hand when it uses an [Anthropic SDK](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation#construct-the-sdk-client) or the `ant` CLI. Point the client at the rule with the federation environment variables and construct it with no arguments; it exchanges the identity token on the first request and re-exchanges it before the access token expires.

```yaml
# .github/workflows/deploy.yml
permissions:
  id-token: write   # lets the job request an OIDC token
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Request the GitHub OIDC token
        run: |
          curl -sS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=https://api.anthropic.com" \
            | jq -r .value > /tmp/anthropic-identity-token

      - name: Run the workload
        env:
          # Plain repository variables: none of these IDs is a secret.
          ANTHROPIC_FEDERATION_RULE_ID: fdrl_...
          ANTHROPIC_SERVICE_ACCOUNT_ID: svac_...
          ANTHROPIC_ORGANIZATION_ID: 00000000-0000-0000-0000-000000000000
          ANTHROPIC_WORKSPACE_ID: wrkspc_...  # only if the rule covers several workspaces
          ANTHROPIC_IDENTITY_TOKEN_FILE: /tmp/anthropic-identity-token
        run: python deploy.py   # anthropic.Anthropic() with no arguments
```

| Variable | Value |
|---|---|
| `ANTHROPIC_FEDERATION_RULE_ID` | The rule's `id` (`fdrl_...`). |
| `ANTHROPIC_SERVICE_ACCOUNT_ID` | The rule's target service account (`svac_...`). |
| `ANTHROPIC_ORGANIZATION_ID` | Your organization UUID. |
| `ANTHROPIC_IDENTITY_TOKEN_FILE` | Path to the platform's OIDC JWT. `ANTHROPIC_IDENTITY_TOKEN` carries the JWT inline instead. |
| `ANTHROPIC_WORKSPACE_ID` | Required only when the rule is enabled for more than one workspace or for all workspaces, to say which one the token is for. |

Leave `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` unset in that job: both take precedence over federation in every SDK. The exact audience to request from GitHub, the flags equivalent to these variables and the `ant` CLI's federation profile (needed when one job runs several `ant` commands, since a GitHub token carrying `jti` is accepted once) are covered in the [GitHub Actions provider guide](https://platform.claude.com/docs/en/manage-claude/wif-providers/github-actions) and the [WIF reference](https://platform.claude.com/docs/en/manage-claude/wif-reference#environment-variables).

None of those three IDs is a secret: a token can only be minted by presenting a JWT from the trusted issuer whose claims match the rule. Store them as plain repository variables.

## 5. Importing Console-created objects

Teams that started with the **Connect workload** wizard must import what it created rather than recreate it: names are unique per organization (a second `gha-deploy` rule is a `409`), and so is an issuer's `issuer_url`, so the `github-actions` issuer the wizard registered is the only one the organization can have for `https://token.actions.githubusercontent.com`, and a `resource "anthropic_federation_issuer"` for that URL only ever succeeds as an import. List the existing objects with the data sources, then import by ID:

```hcl
data "anthropic_federation_issuers" "all" {}
data "anthropic_service_accounts" "all" {}
data "anthropic_federation_rules" "all" {}
```

```shell
terraform import anthropic_federation_issuer.github_actions fdis_01ABC...
terraform import anthropic_service_account.gha_deploy svac_01ABC...
terraform import anthropic_federation_rule.gha_deploy fdrl_01ABC...

# Memberships and enablements use composite IDs.
terraform import 'anthropic_service_account_workspace.gha_deploy_production' svac_01ABC...:wrkspc_01XYZ...
terraform import 'anthropic_federation_rule_workspace.gha_deploy_staging[0]' fdrl_01ABC...:wrkspc_01XYZ...
```

Or, on Terraform 1.5+, declare `import` blocks next to the resources so the import is part of the plan. After importing, run `terraform plan` and reconcile until it is empty; the Console rule with `oauth_scope = "org:admin"` from section 2 can be imported and read but any change to it must still be made in the Console, so keep its configuration identical to what the API returns.

## 6. Operational notes

- **Destroy archives, always.** Issuers, service accounts and rules have no hard-delete endpoint, so `terraform destroy` archives them (`archived_at` is set, the object disappears from lists and its rules stop minting tokens). Archiving is idempotent. Membership (`anthropic_service_account_workspace`) and enablement (`anthropic_federation_rule_workspace`) removals are real deletes.
- **Order of destruction.** Archiving an issuer or a service account returns `400` while a live rule still references it. Terraform's dependency graph gets this right when the rule is declared with references to both, as above; if you imported objects without references, or archive out-of-band, archive the rule first.
- **Names are unique per organization** for each object type, must match `^[a-z0-9-]+$` and be 1 to 255 characters. A duplicate returns `409`. **`issuer_url` is unique too**: the API rejects a second issuer for a URL the organization already trusts, so one issuer per identity provider, shared by all the rules that trust it (including the Console-created bootstrap rule, see section 2). A service account's `name` is immutable and forces replacement; an issuer's and a rule's can be renamed in place.
- **Scopes an OAuth caller may grant.** `workspace:developer` and `workspace:inference` only. `org:admin` and `workspace:manage_tunnels` rules are Console-only, and an issuer backing one of them cannot be updated by the provider either. A rule granting `org:admin` must also target a service account whose `organization_role` is `admin`.
- **Never use a wildcard subject for a privileged rule.** See the warning in section 2; for workspace-scoped rules, prefer an exact `subject_prefix` plus `claims` matches (`repository_owner`, `ref`) over a trailing `*`; if the trailing `*` is unavoidable, pin `ref` in `claims` so pull-request runs from forks never match.
- **Token lifetimes.** `max_jwt_lifetime_seconds` on the issuer bounds the identity token you accept; `token_lifetime_seconds` on the rule bounds the access token you mint, itself capped at twice the identity token's remaining validity. Keep both short for CI.
- **The `org:admin` token used by Terraform expires**, so a plan or apply that runs for a long time may need a fresh export in between. Mint it as late as possible and avoid combining WIF changes with slow resources in the same run.

## See also

- [Manage WIF with the Admin API](https://platform.claude.com/docs/en/manage-claude/wif-admin-api): the endpoints this provider wraps, the bootstrap procedure and the permission constraints.
- [WIF reference](https://platform.claude.com/docs/en/manage-claude/wif-reference): environment variables, credential precedence, validation rules, OAuth scopes and error codes.
- Provider resources: `anthropic_federation_issuer`, `anthropic_service_account`, `anthropic_service_account_workspace`, `anthropic_federation_rule`, `anthropic_federation_rule_workspace`, and the matching data sources.
