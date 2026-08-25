# Renovate

This project uses [Renovate](https://docs.renovatebot.com/) to automate dependency updates, via the
**[Mend-hosted Renovate app](https://docs.renovatebot.com/mend-hosted/overview/)** installed at the
`ippontech` GitHub organization level.

## How it works

Nothing runs in this repository's CI. Mend hosts the bot and drives it:

1. It scans the repo on a regular cadence (every 4 hours on the Community plan) and also reacts to
   GitHub webhooks — a merged Renovate PR, for instance, triggers a re-scan of the remaining branches.
2. It reads `renovate.json` from the repository root and opens pull requests for outdated dependencies.
3. Runs can be triggered on demand from the [Mend Developer Portal](https://developer.mend.io/) or by
   ticking a checkbox on the Dependency Dashboard issue.

Commits are made through the GitHub API and therefore appear as **verified**, authored by `renovate[bot]`.

## Configuration

A single file drives the bot: `renovate.json` at the repository root. Org-wide defaults, plus enabling
or disabling this repo, live in the Mend Developer Portal.

| Setting | Value |
|---|---|
| `extends` | `config:recommended`, `:semanticCommits`, `:dependencyDashboard` |
| `labels` | `dependencies` |
| `schedule` | `before 6am on Monday` — gates when branches are created, so PR noise stays weekly even though the bot scans several times a day |
| `prConcurrentLimit` | 10 open Renovate PRs at a time |
| `platformAutomerge` | Uses GitHub's own auto-merge, so branch protection and required checks are respected |
| `enabledManagers` | `github-actions`, `gomod`, `mise`, `pre-commit`, `terraform` |
| `packageRules` | One grouped PR per manager, and automerge for `minor` / `patch` / `pin` / `digest` updates |

Every option above is repository-level config. Global options (`platform`, `repositories`, `onboarding`,
credentials) belong to the bot host, so they must not be added here:
[Renovate ignores them](https://docs.renovatebot.com/self-hosted-configuration/) and may open a config
error issue on the repo.

## Validating a config change

Renovate ships a validator that catches unknown keys and malformed values:

```bash
npx --yes --package renovate -- renovate-config-validator renovate.json
```

Note it validates the file as *global* config, so it will not flag a global-only option that has been
misplaced into repository config — that only shows up as a config error issue on the next hosted run.
The `$schema` key in `renovate.json` also gives editors inline completion and validation.

## History

Until [#174](https://github.com/ippontech/terraform-provider-anthropic/issues/174), Renovate was
self-hosted in this repo: a weekly cron workflow (`.github/workflows/renovate.yml`) ran
[`renovatebot/github-action`](https://github.com/renovatebot/github-action), authenticating as a
repo-dedicated GitHub App (`tf-provider-anthropic-renovate`) whose credentials lived in the
`RENOVATE_APP_ID` / `RENOVATE_APP_PRIVATE_KEY` repository secrets. That setup existed to get signed
commits; the hosted app provides them out of the box. The workflow and its runner-level
`.github/renovate-config.json` are gone, and the App and its secrets have been decommissioned.
