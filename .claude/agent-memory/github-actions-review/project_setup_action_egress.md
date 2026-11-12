---
name: Setup actions download what the runner image lacks
description: A runtime major the runner image does not preinstall makes setup-node/setup-go download it, which harden-runner blocks unless release-assets.githubusercontent.com is allowed
type: project
---

Learned on 2026-11-12 from the Node 24 -> 26 bump (#495), which broke every Semantic Release run for three days (fixed in #572).

- `actions/setup-node` (and `setup-go`) use the runner image's toolcache when it has the requested version, and reach no network host. For a version the image does not ship, they download it from the `actions/node-versions` (`actions/go-versions`) release assets: `github.com` redirects to `release-assets.githubusercontent.com`, then `nodejs.org` (`go.dev`, `dl.google.com`) as fallback.
- So a Renovate PR that bumps `node-version` or the Go `toolchain` past what `ubuntu-latest` preinstalls needs `release-assets.githubusercontent.com:443` in that job's `allowed-endpoints`. When reviewing such a bump, check the endpoint list of every job running the setup action.
- `semantic-release.yml` runs only on schedule and `workflow_dispatch`, never on `pull_request`, so a break there passes the PR's checks and shows up only in the next scheduled run.
