---
name: project-go-toolchain-mismatch
description: "Fix for \"version goX.Y.Z does not match go tool version\" when running go build/test/make in a worktree"
metadata:
  type: project
---

`go build`/`go test`/`make` can fail with `compile: version "go1.27.0" does not match go tool version "go1.26.2"` (or similar) across the whole stdlib, even in a fresh worktree. Cause: a `toolchain@*` binary under `$GOPATH/pkg/mod/golang.org/toolchain@v0.0.1-goX.Y.Z/bin/go` sits ahead of the mise-managed `go` on PATH (or `GOROOT` points at a different patch version than the invoked compiler), so the compiler and the GOROOT stdlib disagree.

**How to apply:** before any `go`/`make` command in the worktree:
```
export GOTOOLCHAIN=local
export PATH="$HOME/.local/share/mise/installs/go/<version>/bin:$PATH"
unset GOROOT
```
Find `<version>` with `ls ~/.local/share/mise/installs/go/` — pick the concrete version (not `latest`/the bare major.minor symlink). It will not necessarily match the `go` directive in `go.mod` (newer is fine with `GOTOOLCHAIN=local`).

Also do NOT `source .env` directly in a worktree-isolated Bash session — the harness blocks piping an unverified file through `source`. Read `.env`'s contents first (`cat .env`) and `export` the variables literally instead.

Separately, `pre-commit run -a`'s `poutine` hook can fail with `mise ERROR ... not trusted` on a freshly checked-out worktree with its own `mise.toml` — run `mise trust` once in the worktree root to fix.
