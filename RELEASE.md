# GPG Key Setup

The release workflow signs the provider checksums with a GPG key. This is required by the [Terraform Registry](https://registry.terraform.io) to verify the authenticity of provider binaries.

## Credentials storage

The GPG key (private key and passphrase) is stored in Ippon's Vaultwarden instance under the **Cloud & DevOps** organization under **Terraform Anthropic provider** folder.

## Generate a GPG key

```bash
gpg --full-generate-key
```

When prompted:

| Prompt | Value |
|--------|-------|
| Key type | `1` (RSA and RSA) |
| Key size | `4096` |
| Expiry | `0` (no expiration) |
| Name / Email | Your identity (visible in the Terraform Registry) |
| Passphrase | A strong passphrase |

## Retrieve the key ID

```bash
gpg --list-secret-keys --keyid-format LONG
```

The key ID is the part after `rsa4096/`:

```
sec   rsa4096/AAAA1111BBBB2222 2024-01-01 [SC]
             ^^^^^^^^^^^^^^^^^
             key ID
```

## Configure GitHub secrets

Add the following secrets to the GitHub repository (`Settings > Secrets and variables > Actions`):

| Secret | Command to get the value |
|--------|--------------------------|
| `GPG_PRIVATE_KEY` | `gpg --armor --export-secret-keys YOUR_KEY_ID` |
| `GPG_PASSPHRASE` | The passphrase chosen during key generation |

## Register the public key in the Terraform Registry

Export the public key:

```bash
gpg --armor --export YOUR_KEY_ID
```

Then paste it in your Terraform Registry namespace:
`registry.terraform.io > Settings > GPG Keys > Add a GPG key`

# GitHub App Setup (Semantic Release)

The semantic-release workflow uses a GitHub App to push the `chore(release):` commit and tags directly to `main`, bypassing the branch ruleset (PR requirement, merge queue, and signed commits).

## Create the App

Go to your GitHub organization (or personal account) → **Settings** → **Developer settings** → **GitHub Apps** → **New GitHub App**

Fill in:

| Field | Value |
|-------|-------|
| Name | e.g. `tf-provider-anthropic-semrel` |
| Homepage URL | Your repository URL (required, unused) |
| Webhooks | Uncheck **Active** |

**Repository permissions** (set to Read & write unless noted):

| Permission | Access |
|------------|--------|
| Contents | Read & write |
| Issues | Read & write |
| Pull requests | Read & write |
| Metadata | Read only (mandatory) |

Click **Create GitHub App**.

## Generate a private key

On the app's settings page, scroll to **Private keys** → **Generate a private key**. A `.pem` file will download — store it securely (e.g. in Vaultwarden under **Terraform Anthropic provider**).

## Note the App ID

The **App ID** (a number) is shown at the top of the app's settings page.

## Install the App on the repository

On the app's settings page → **Install App** → select your org/account → **Only select repositories** → pick this repository → **Install**.

## Configure GitHub secrets

Add the following secrets to the repository (`Settings > Secrets and variables > Actions`):

| Secret                   | Value                                      |
|--------------------------|--------------------------------------------|
| `SEMREL_APP_ID`          | The numeric App ID from the previous step  |
| `SEMREL_APP_PRIVATE_KEY` | The full contents of the `.pem` file       |

## Add the App as a bypass actor in the main branch ruleset

This allows the App to push directly to `main` without going through the PR/merge queue flow and without GPG-signed commits.

1. Go to **Settings** → **Rules** → **Rulesets**
2. Click the ruleset that applies to `main`
3. Scroll to **Bypass list** → click **Add bypass**
4. Select type **GitHub App**, search for and select the app created above
5. Set the bypass mode to **Always**
6. Save

# Major versions

Breaking changes are batched into a single major release, at most once a year, as HashiCorp recommends ([provider versioning best practices](https://developer.hashicorp.com/terraform/plugin/best-practices/versioning)). Between two majors, breaking changes accumulate on a long-lived staging branch, the way the Google provider stages them on `FEATURE-BRANCH-major-release-X.0.0` ([Magic Modules: make a breaking change](https://googlecloudplatform.github.io/magic-modules/breaking-changes/make-a-breaking-change/)). The staging branch never releases anything: semantic-release only runs on `main` (see `.github/workflows/semantic-release.yml`) and `.releaserc` pins `branches` to `["main"]`, so no branch name can accidentally match one of semantic-release's default release branches (`next`, `next-major`, `beta`, `alpha`, `N.x`).

The first major planned this way is 2.0.0; candidates are the issues labelled as breaking changes, e.g. [#187](https://github.com/ippontech/terraform-provider-anthropic/issues/187) and [#91](https://github.com/ippontech/terraform-provider-anthropic/issues/91).

## Staging branch

1. Create `major/2.0.0` from `main`. Do **not** add it to the semantic-release workflow trigger or to `.releaserc`.
2. The `major-upgrades` ruleset (`refs/heads/major/*`) requires the same status checks as `main` but no pull request and no merge queue. It must **not** require linear history: syncs from `main` (step 4) are merge commits. Because the CI workflows only run on `pull_request` and `merge_group`, a direct push never carries status checks and is rejected, so every change to the branch, syncs included, goes through a PR. The repository therefore keeps **Allow merge commits** enabled alongside squash; this does not weaken `main`, whose ruleset restricts merges to squash on its own.
3. Open every breaking-change PR against `major/2.0.0`. Squash-merge it with a `feat!:` / `fix!:` type or a `BREAKING CHANGE:` footer that states what changed and how to migrate. That text is what ends up in the 2.0.0 release notes, so write it for users, not for reviewers.
4. Keep the branch in sync with `main` at least after every release on `main`: open a PR `main` → `major/2.0.0` (from a throwaway `sync/major-2.0.0` branch if conflicts need resolving) and merge it with a **merge commit**, never squash. Resolve conflicts in favour of the breaking change, never by reverting it. The merge keeps `main`'s commits as ancestors, which is what lets the final merge below go through cleanly.
5. Write the upgrade guide as you go, in `templates/guides/version_2_upgrade.md.tmpl` on the staging branch, one section per breaking change. Every `BREAKING CHANGE:` footer should have a matching section.

## Cutting the release

1. Merge `main` into `major/2.0.0` one last time and make sure `make` and `make terraform-test` pass there.
2. Temporarily edit the `main` ruleset (`Settings > Rules > Rulesets`): add **Merge** to the allowed merge methods of the pull request rule, untick **Require linear history**, and disable the **merge queue** rule (it squashes). A squash would flatten every breaking-change commit into one message, losing the `BREAKING CHANGE:` footers that drive the version bump and the notes.
3. Open a PR `major/2.0.0` → `main` and merge it with a **merge commit**. semantic-release then analyses every commit since the last `v1.x` tag, finds the `BREAKING CHANGE:` footers, publishes a single `v2.0.0` and lists each breaking change in the release notes and in `CHANGELOG.md`.
4. Restore the `main` ruleset (squash only, linear history, merge queue) and delete `major/2.0.0`.

If a `1.x` maintenance line is needed after 2.0.0, create `1.x` from the last `v1.*` tag, add it to the workflow trigger and to `.releaserc` (`"branches": ["1.x", "main"]`); semantic-release then restricts it to `1.*` versions and refuses anything that would collide with `main`.

Pre-releases (`2.0.0-beta.N`, the approach the AWS provider used on `release/6.0.0-beta`) are deliberately not part of this process: they would require `@semantic-release/git` to commit `CHANGELOG.md` on the staging branch, which conflicts on every sync with `main`. Revisit only if users ask to test a beta from the Registry.
