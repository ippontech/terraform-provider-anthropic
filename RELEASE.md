# How a release is cut

Every push to `main` runs `semantic-release.yml`:

1. The acceptance tests (`testacc.yml`, called as a reusable workflow) run against the real API with the keys of the `acctest` environment. They run after the merge because a pull request from a fork cannot read those keys.
2. Only if they pass, semantic-release reads the commits since the last tag. A `feat`, `fix`, `perf`, `refactor`, `build` or `docs` commit, or a `BREAKING CHANGE` footer, cuts a version: semantic-release pushes the `chore(release):` commit with `CHANGELOG.md`, the `v*` tag and the GitHub release. Other commit types (`chore`, `ci`, `test`, ...) cut nothing.
3. The tag starts `goreleaser-release.yml`, which builds, signs and attaches the binaries once a maintainer approves it (see [Protect the release credentials](#protect-the-release-credentials)).

A red acceptance run publishes nothing. Fix the cause, or, if the API itself was failing, re-run the failed jobs of the most recent run on `main` only: an older run would take the place of a newer pending run, whose commit would then go untested, and its release would publish nothing since `main` has moved on. The next successful run releases every commit still pending. `Actions > Acceptance Tests > Run workflow` still runs the tests alone, without releasing.

The workflow runs one at a time on `main` (`concurrency` group, no cancellation): when several commits land while a run is testing, only the newest waiting one runs. An intermediate commit then gets no acceptance run of its own; its changes are tested as part of the newer commit. The run that was testing publishes nothing, even when green, because semantic-release publishes only from the head of `main`; the newer run releases every pending commit once its own tests pass. A running test is never cancelled to make room: the objects it had created would stay in the test workspace, since nothing deletes them afterwards.

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

Add the following secrets to the **`release` environment** (`Settings > Environments > release > Environment secrets`), never at repository or organization level: a repository secret is readable by a workflow pushed on any branch. The environment itself is set up in [Protect the release credentials](#protect-the-release-credentials).

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

`semantic-release.yml` requests exactly these three write permissions when it mints the token (`permission-*` inputs of `actions/create-github-app-token`). A permission added to the App later does not reach the token, and if the App lacks one of the three, the token step fails.

Click **Create GitHub App**.

## Generate a private key

On the app's settings page, scroll to **Private keys** → **Generate a private key**. A `.pem` file will download — store it securely (e.g. in Vaultwarden under **Terraform Anthropic provider**).

## Note the App ID

The **App ID** (a number) is shown at the top of the app's settings page.

## Install the App on the repository

On the app's settings page → **Install App** → select your org/account → **Only select repositories** → pick this repository → **Install**.

## Configure GitHub secrets

Add the following secrets to the **`semantic-release` environment** (`Settings > Environments > semantic-release > Environment secrets`), never at repository or organization level, for the same reason as the GPG key:

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

# Protect the release credentials

The GPG key signs what every user installs, and the App bypasses the `main` ruleset. Write access to the repository must not be enough to use either: a collaborator, or a stolen collaborator token, can push a workflow file on a new branch, and that workflow receives every repository secret; and any write collaborator can push a `v*` tag on an arbitrary commit, which starts the release job. Three settings close this. The workflows already declare the environments, so they keep working at every step of the setup below (a job falls back to the repository secret while its environment has none).

## Environments

Create two environments (`Settings > Environments > New environment`):

| Environment | Used by | Deployment branches and tags | Secrets |
|---|---|---|---|
| `release` | `goreleaser-release.yml` | **Selected branches and tags**, one tag rule `v*` and no branch rule | `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE` |
| `semantic-release` | `semantic-release.yml` | **Selected branches and tags**, one branch rule `main` | `SEMREL_APP_ID`, `SEMREL_APP_PRIVATE_KEY` |
| `acctest` (already set up) | `testacc.yml`, which `semantic-release.yml` calls before releasing | **Selected branches and tags**, one branch rule `main` | `ANTHROPIC_API_KEY`, `ANTHROPIC_ADMIN_API_KEY`, `ANTHROPIC_MULTI_WORKSPACE_API_KEY` |

On `release`, also tick **Required reviewers** and add the maintainers: every signed release then waits for one approval in the Actions UI, which is the only control that holds even if a tag slips through. Leave **Allow administrators to bypass** unticked.

Then **delete the four secrets at repository level** (`Settings > Secrets and variables > Actions`), and check that no organization secret carries them. Until they are deleted, the environments protect nothing.

## Tag ruleset

Create a tag ruleset (`Settings > Rules > Rulesets > New ruleset > New tag ruleset`):

1. Name it `release-tags`, enforcement **Active**.
2. Target tags: include by pattern `v*`.
3. Rules: **Restrict creations**, **Restrict updates**, **Restrict deletions**.
4. Bypass list: the semantic-release App only, mode **Always**. No role, team or collaborator.

semantic-release keeps tagging releases; nobody else can create, move or delete a `v*` tag.

## Check

1. Push a branch with a workflow that prints `${{ secrets.GPG_PASSPHRASE != '' }}`: it must print `false`. Delete the branch.
2. From a collaborator account, `git push origin <any-commit>:refs/tags/v9.9.9`: GitHub must refuse it.
3. On the next release, the `goreleaser-release` run waits for a reviewer before its job starts.

The release job also refuses a tagged commit that is not on `main` (`git merge-base --is-ancestor`). That check is defence in depth only: a tag-triggered run reads its workflow file from the tagged commit, so whoever can tag an arbitrary commit can remove the check too.

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

If a `1.x` maintenance line is needed after 2.0.0, create `1.x` from the last `v1.*` tag, add it to the workflow trigger and to `.releaserc` (`"branches": ["1.x", "main"]`); semantic-release then restricts it to `1.*` versions and refuses anything that would collide with `main`. Three guards admit `main` only and must be widened at the same time, or `1.x` gets no release and no signed artifacts: add a `1.x` branch rule to the deployment branches of the `semantic-release` environment and of the `acctest` environment, whose acceptance tests every release waits for (`Settings > Environments`), and extend the "Check the tagged commit is on main" step of `goreleaser-release.yml` to accept a commit on `1.x` as well.

Pre-releases (`2.0.0-beta.N`, the approach the AWS provider used on `release/6.0.0-beta`) are deliberately not part of this process: they would require `@semantic-release/git` to commit `CHANGELOG.md` on the staging branch, which conflicts on every sync with `main`. Revisit only if users ask to test a beta from the Registry.
