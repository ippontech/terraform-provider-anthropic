# Renovate

This project uses [Renovate](https://docs.renovatebot.com/) via the [`renovatebot/github-action`](https://github.com/renovatebot/github-action) to automate dependency updates.

## How it works

The workflow (`.github/workflows/renovate.yml`) runs every Monday at 4 AM UTC and can also be triggered manually via `workflow_dispatch`. It:

1. Generates a short-lived token from the `tf-provider-anthropic-renovate` GitHub App.
2. Passes that token to the Renovate runner, which opens pull requests for outdated dependencies.

## GitHub App

Renovate authenticates as the **`tf-provider-anthropic-renovate`** GitHub App instead of a personal access token. This ensures that Renovate commits are **signed** (verified badge on GitHub) and attributed to the bot rather than a human account.

See the [Renovate GitHub App documentation](https://docs.renovatebot.com/modules/platform/github/#running-as-a-github-app) for the full setup guide.

Two repository secrets are required:

| Secret | Description |
|---|---|
| `RENOVATE_APP_ID` | Numeric ID of the GitHub App |
| `RENOVATE_APP_PRIVATE_KEY` | PEM private key generated for the GitHub App |

The workflow uses [`actions/create-github-app-token`](https://github.com/actions/create-github-app-token) to exchange these credentials for a short-lived installation token at runtime. Setting `RENOVATE_PLATFORM_COMMIT: 'true'` tells Renovate to commit via the GitHub API (rather than git over SSH/HTTPS), which is what causes commits to appear as signed by the app.

## Configuration files

| File | Purpose |
|---|---|
| `renovate.json` | Repository-level Renovate config (schedule, managers, grouping rules) |
| `.github/renovate-config.json` | Runner-level config consumed by `renovatebot/github-action` |
| `.github/workflows/renovate.yml` | GitHub Actions workflow that runs the Renovate job |

### `renovate.json`

- **Schedule**: before 6 AM on Monday (aligns with the workflow cron).
- **Dependency dashboard**: enabled — Renovate opens a tracking issue listing pending updates.
- **PR concurrent limit**: 10 open Renovate PRs at a time.
- **Enabled managers**: `github-actions`, `gomod`, `mise`, `pre-commit`, `terraform`.
- **Grouping rules**: related updates are batched into a single PR per manager to reduce noise.

### `.github/renovate-config.json`

Runner-level options passed to the self-hosted Renovate process:

- `branchPrefix: "renovate/"` — all update branches are prefixed with `renovate/`.
- `gitAuthor` — the commit author displayed on Renovate PRs.
- `onboarding: true` — Renovate will open an onboarding PR when added to a new repository.
- `platform: "github"` — explicitly set to GitHub.
- `repositories: []` — repositories to manage; populated at runtime by the workflow context.
