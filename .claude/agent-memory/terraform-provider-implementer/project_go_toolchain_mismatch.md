---
name: project-go-toolchain-mismatch
description: "Fix for \"version goX.Y.Z does not match go tool version\" when running go build/test/make in a worktree"
metadata:
  type: project
---

Status: reproduced on 2026-11-08 by running a Go 1.26.2 `go` binary with `GOROOT` pointing at a Go 1.27.3 install (`GOROOT=<go1.27.3 root> <go1.26.2 root>/bin/go build ./internal/errors/`): `compile: version "go1.27.3" does not match go tool version "go1.26.2"` on every standard-library package; the same build passes once `GOROOT` is unset and the `go` found first matches the toolchain. Diagnose with `go version; go env GOROOT GOTOOLCHAIN; which -a go` (the `go` found first must be the mise-managed one). The separate `go clean -cache` fix after a Go upgrade is in `CLAUDE.md`.

`go build`/`go test`/`make` can fail with `compile: version "go1.27.0" does not match go tool version "go1.26.2"` (or similar) across the whole stdlib, even in a fresh worktree. Cause: a `toolchain@*` binary under `$GOPATH/pkg/mod/golang.org/toolchain@v0.0.1-goX.Y.Z/bin/go` sits ahead of the mise-managed `go` on PATH (or `GOROOT` points at a different patch version than the invoked compiler), so the compiler and the GOROOT stdlib disagree.

**How to apply:** each Bash call starts a fresh shell, so `export` and `unset` do not carry over to the next command. Put the settings on every `go`/`make` command instead, next to the `GOBIN` prefix:
```
env -u GOROOT GOTOOLCHAIN=local PATH="$HOME/.local/share/mise/installs/go/<version>/bin:$PATH" GOBIN="$PWD/.gobin" make ...
```
Find `<version>` with `ls ~/.local/share/mise/installs/go/` — pick the concrete version (not `latest`/the bare major.minor symlink). It will not necessarily match the `go` directive in `go.mod` (newer is fine with `GOTOOLCHAIN=local`).

Separately, `pre-commit run -a`'s `poutine` hook can fail with `mise ERROR ... not trusted` on a freshly checked-out worktree with its own `mise.toml` — run `mise trust` once in the worktree root to fix.
