---
name: project-sdk-param-opt
description: How to assert that an SDK request field is an explicit JSON null rather than omitted, in unit tests
metadata:
  type: project
---

Checked on 2026-10-25 against anthropic-sdk-go v1.67.0 (the version pinned in `go.mod`).

`param.Opt[T]` has three states: a value (`param.NewOpt(v)`), an explicit JSON `null` (`param.Null[T]()`) and omitted (the zero value `param.Opt[T]{}`). `Opt.Valid()` returns `false` for both null and omitted, so a test that must prove a field is sent as `null` (to clear it server-side) uses `param.IsNull(v)`, and one that must prove it is left out uses `param.IsOmitted(v)`. Both are exported by `github.com/anthropics/anthropic-sdk-go/packages/param`; there is no need for a hand-written `MarshalJSON` check.

Re-check: `grep -n "func IsNull\|func IsOmitted" "$(go env GOMODCACHE)"/github.com/anthropics/anthropic-sdk-go@v1.67.0/packages/param/param.go`
