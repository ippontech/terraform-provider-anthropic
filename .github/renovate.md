# Renovate

This project uses [Renovate](https://docs.renovatebot.com/) to automate dependency updates, via the
**[Mend-hosted Renovate app](https://docs.renovatebot.com/mend-hosted/overview/)** installed at the
`ippontech` GitHub organization level.

## How it works

Nothing runs in this repository's CI. Mend hosts the bot and drives it:

1. It scans the repo on a regular cadence (every 4 hours on the Community plan) and also reacts to
   GitHub webhooks — a merged Renovate PR, for instance, triggers a re-scan of the remaining branches.
2. It reads `renovate.json` from the repository root and opens pull requests for outdated dependencies.
3. Runs can be triggered on demand from this repository's
   [Mend dashboard](https://developer.mend.io/github/ippontech/terraform-provider-anthropic) or by
   ticking a checkbox on the Dependency Dashboard issue.

Commits are made through the GitHub API and therefore appear as **verified**, authored by `renovate[bot]`.

## Configuration

A single file drives the bot: `renovate.json` at the repository root. Org-wide defaults, plus enabling
or disabling this repo, live in the Mend Developer Portal —
[this repository's page](https://developer.mend.io/github/ippontech/terraform-provider-anthropic) also
shows its recent runs and their logs.

| Setting | Value |
|---|---|
| `extends` | `config:recommended`, `:semanticCommits`, `:dependencyDashboard`, `:enablePreCommit` |
| `labels` | `dependencies` |
| `platformAutomerge` | Uses GitHub's own auto-merge, so branch protection and required checks are respected. This is already the default; kept explicit because the whole automerge policy depends on it |
| `osvVulnerabilityAlerts` | Checks every dependency version in use against [OSV](https://osv.dev/) and opens a dedicated `[SECURITY]` fix PR, ungrouped. It also covers the Go modules marked `// indirect` in `go.mod`, which Renovate otherwise never updates, and the Go standard library through the `toolchain` directive (see [Go version](#go-version)). Experimental upstream |
| `vulnerabilityAlerts` | Settings Renovate forces on those `[SECURITY]` PRs, over every package rule. `automerge: false`: a maintainer reviews and merges each one. `minimumReleaseAge: 3 days`: the cooldown every other update gets, which Renovate's defaults drop for security PRs ([docs](https://docs.renovatebot.com/key-concepts/minimum-release-age/#what-happens-to-security-updates)). Until then the fix waits under *Pending Status Checks* on the Dependency Dashboard, whose checkbox creates the PR at once |
| `enabledManagers` | `github-actions`, `gomod`, `mise`, `pre-commit`, `custom.regex`, `npm` |
| `customManagers` | Two regex managers, see [Custom managers](#custom-managers) below |
| `packageRules` | One grouped PR per manager, automerge for `minor` / `patch` / `pin` / `digest` updates after a 3-day `minimumReleaseAge` cooldown, except release tooling (see [Release tooling](#release-tooling)), plus a version hold on the Anthropic Go SDK's next major and a reviewed PR for its minor releases (see [Version holds](#version-holds)) |

Two of these entries are less obvious than they look:

- **`:enablePreCommit` is required.** The `pre-commit` manager is `enabled: false` by default (it is still
  flagged beta upstream). Listing it in `enabledManagers` only filters *which of the enabled managers*
  run; it does not switch on a manager that ships disabled. Without this preset the `rev:` pins in
  `.pre-commit-config.yaml` are never updated, silently.
- **`custom.regex` must be in `enabledManagers`.** That is the manager name custom managers run under.
  Because `enabledManagers` is set at all, omitting it would stop every entry in `customManagers` from
  running, again silently.

There is deliberately **no `terraform` manager**. `config:recommended` pulls in `:ignoreModulesAndTests`,
whose `ignorePaths` cover `**/examples/**` and `**/tests/**` — and every `.tf` file in this repository
lives under one or the other. The manager would have nothing to scan. That is the outcome we want:
example configs pin `~> 1.0` on purpose (see `CLAUDE.md`), so they must not be bumped to the latest
published provider version.

`prConcurrentLimit` is not set either: 10 is already the default.

There is deliberately **no `schedule`** (nor the `timezone` that only existed to anchor it). Both were
removed in [#182](https://github.com/ippontech/terraform-provider-anthropic/pull/182): a schedule gates
when branches may be created, and combined with the hosted app's own 4-hour scan cadence it could skip
the window entirely on a given day. Updates now arrive whenever Mend next scans, and the 3-day
`minimumReleaseAge` remains the only deliberate delay — enough to keep a breaking bump easy to attribute.

Every option above is repository-level config. Global options (`platform`, `repositories`, `onboarding`,
credentials) belong to the bot host, so they must not be added here:
[Renovate ignores them](https://docs.renovatebot.com/self-hosted-configuration/) and may open a config
error issue on the repo.

## Custom managers

The GoReleaser version is not visible to a built-in manager, so it was drifting unnoticed:

| What | Where | How it is tracked |
|---|---|---|
| GoReleaser CLI | `version:` input of `goreleaser/goreleaser-action` in `goreleaser-check.yml` and `goreleaser-release.yml` | `# renovate:` annotation comment on the preceding line |

The `github-actions` manager updates `uses:` refs (including digests of SHA-pinned actions) and a few
well-known inputs such as `setup-node`'s `node-version`, but it does not read arbitrary action inputs —
hence the annotation. To put another tool version under Renovate's control, add the same comment above it:

```yaml
        with:
          # renovate: datasource=github-releases depName=owner/repo
          version: v1.2.3
```

It is grouped into the `CI tool versions` PR, except for the version pinned in the release workflow,
which goes to the `release tooling` PR below.

The semantic-release toolchain is a regular npm project under `.github/semantic-release/`
(`package.json` with exact versions, `package-lock.json` with integrity hashes, `.npmrc` with
`ignore-scripts=true`), tracked by the built-in `npm` manager, which regenerates the lockfile in the
same PR. The workflow installs it with `npm ci --ignore-scripts`, so a new release of a transitive
package changes nothing until a reviewed PR updates the lockfile.

## Release tooling

Every dependency declared in `goreleaser-release.yml` and `semantic-release.yml` runs next to a release
credential: the semantic-release GitHub App token, which bypasses the `main` ruleset, and the GPG key
that signs the checksums the Terraform Registry verifies. A malicious patch release of any of them would
otherwise be merged by GitHub auto-merge three days after publication, with no maintainer decision, and
run with those credentials on the next push or tag.

A `matchFileNames` rule therefore moves these updates (GoReleaser, the semantic-release packages and
their lockfile, and the actions used by the two workflows) into their own `release tooling` group with `automerge: false`. A
maintainer reviews the release notes and the diff of each one before merging. The rule matches by file,
not by package, so the same action used in other workflows (`actions/checkout`, say) keeps its
automerged `GitHub Actions` group there; only its release-workflow occurrence waits for review.

## Go version

`go.mod` carries two Go versions, and Renovate treats them differently:

| Directive | Meaning | Renovate |
|---|---|---|
| `go 1.26.2` | Language version: the oldest Go that can build the module, and the `GODEBUG` defaults compiled into the binary | Never proposes an update, by design ([gomod manager docs](https://docs.renovatebot.com/modules/manager/gomod/#updating-of-go-mod-and-toolchain-directives)). Raise it by hand, deliberately |
| `toolchain go1.26.10` | The Go release CI tests with and releases are built with: `actions/setup-go` reads it before the `go` directive | Proposes updates by default, inside the `Go dependencies` group. `osvVulnerabilityAlerts` matches it against the standard-library advisories; it skips the `go` directive |

Updates of the `toolchain` line (dependency type `toolchain`), and of the modules marked `// indirect`, are committed as `fix(deps)`, like direct modules, so semantic-release publishes a patch release that carries them. The `:semanticPrefixFixDepsChoreOthers` preset of `config:recommended` would type them `chore(deps)`, which publishes nothing ([preset](https://docs.renovatebot.com/presets-default/#semanticprefixfixdepschoreothers), [dependency types](https://docs.renovatebot.com/modules/manager/gomod/#dependency-types)). The `go` line (dependency type `golang`) keeps the preset's `chore(deps)`; Renovate does not propose updates to it anyway (see the table above). The rule matches the root `go.mod` only (`matchFileNames`): `tools/go.mod` feeds `make generate` and is not linked into the binary, so its indirect updates stay `chore(deps)`.

The `toolchain` line is what keeps the standard-library vulnerabilities out of the released binaries, so
leave it in place: `go mod tidy` removes it as soon as it is not newer than the `go` directive.

Expect Renovate to propose the latest Go release for the toolchain, a minor update when a new Go minor is
out. golangci-lint refuses to analyse a module whose toolchain is a newer Go minor than the one it was
built with, so such a PR stays red on the `lint` job, and is not automerged, until the golangci-lint used
by CI is built with that Go.

## Version holds

One dependency is deliberately capped rather than tracked to latest:

| Dependency | Cap | Why |
|---|---|---|
| `github.com/anthropics/anthropic-sdk-go` | `<2.0.0` | v2 moves the client to API version `2026-10-21`, which is breaking for this provider: `temperature`, `api_keys.workspace_id` and the rate-limit `group_type` are removed, skills are served in their GA shape only, and the API key is sent as `Authorization: Bearer` instead of `x-api-key`. The migration is [#192](https://github.com/ippontech/terraform-provider-anthropic/issues/192), on the `major/2.0.0` staging branch |

The cap keeps that major update off `main`: breaking changes go through `major/2.0.0` (see `RELEASE.md`,
section "Major versions"), and a v2 PR opened against `main` would only be noise, or worse, get merged
there. The v1 line itself is no longer held: the provider runs on v1.84.0 with no change on the wire.

Until that bump the cap was `<1.68.0`. v1.68.0 had retyped `client.Beta.Skills` to the GA shape and
stopped sending the `skills-2025-10-02` header, and the provider read the skills attributes from the typed
fields, so a plain bump would have read empty values. The provider now sends the header itself and decodes
the beta shape from the raw response (`internal/services/skills/skill_beta_shape.go`), so that hold was
lifted together with the code change rather than on its own.

Three consequences worth knowing:

- `allowedVersions` filters the version out entirely, so the SDK will keep appearing **up to date** on
  the Dependency Dashboard while v2 releases ship. #192 is the reminder, not the dashboard.
- v1 patch releases still automerge with the `Go dependencies` group. v1 minor releases (v1.84.0 is the
  last one at the time of writing) get a PR of their own, `Anthropic Go SDK`, which is never automerged:
  three v1 minors changed what goes over the wire without breaking the build (v1.68.0 stopped sending the
  skills beta header, v1.75.0 started sending extra `input_schema` keys, v1.80.0 turned `multiagent` into
  a union), and no CI job calls the live API, so a green build says nothing about them. The PR is separate
  because a group automerges only when every update in it does.
- An `osvVulnerabilityAlerts` PR brings its own `allowedVersions` (the fixed versions), which Renovate appends
  after `packageRules` and which replaces the hold. A second rule therefore always disables the SDK's `major`
  updates, a new major being the #192 migration: a fix released only in v2 gets no `[SECURITY]` PR, the weekly
  `govulncheck` run reports it if the provider reaches the vulnerable code, and it goes through #192.

Remove the rule as part of #192, in `major/2.0.0` — never on its own.

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

Switching apps also changed the bot's git identity, from `tf-provider-anthropic-renovate[bot]` to
`renovate[bot]`. Renovate decides whether a branch is safe to update by comparing its last commit author
against its own `gitAuthor`, so every Renovate branch still open at the time of the migration was
classified as **PR Edited (Blocked)** on the new Dependency Dashboard: never rebased, never bumped, never
automerged, never closed. Worse, the branch name stays taken, so the update it carried cannot be reopened
under a fresh PR. The fix is to tick that PR's checkbox in the *PR Edited (Blocked)* dashboard section,
which discards the old commits and recreates the branch under the new identity. Expect this once, for
in-flight PRs only. The old dashboard issue is likewise orphaned and has to be closed by hand.
