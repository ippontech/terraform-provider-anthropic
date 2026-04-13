---
name: CI workflow coverage map
description: Full mapping of GNUmakefile targets to ci.yml jobs, confirmed coverage status, and job dependency chain
type: project
---

Makefile target to CI job mapping for terraform-provider-anthropic (audited 2026-04-13):

| Makefile Target | What It Does | CI Job (file: .github/workflows/ci.yml) | Covered? |
|---|---|---|---|
| make (default) | fmt + lint + install + generate | N/A (dev convenience) | Intentionally excluded |
| make build | go build | build job | Yes |
| make install | go install to GOPATH/bin | build job (via make install step) | Yes |
| make fmt | gofmt formatting | lint job (golangci-lint includes fmt) | Yes |
| make lint | golangci-lint | lint job | Yes |
| make test | unit tests, 120s timeout, 10 parallel | unit-test job | Yes |
| make testacc | acceptance tests (TF_ACC=1, 120m timeout) | acceptance-test job | Yes |
| make generate | regenerate docs, format examples | N/A (doc generation, intentionally excluded) | Intentionally excluded |
| make terraform-test | terraform init + terraform test via dev overrides | terraform-test job | Yes — FULLY COVERED |

Job dependency chain in ci.yml:
- build (no needs)
- lint (no needs)
- unit-test (no needs)
- acceptance-test: needs build
- terraform-test: needs [build, acceptance-test]

terraform-test job details (confirmed via Edit probing 2026-04-13):
- runs-on: ubuntu-latest
- needs: [build, acceptance-test]
- env: ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
- steps: checkout, setup-go (go-version-file: go.mod), setup-terraform (terraform_wrapper: false), run make terraform-test
- trigger: on push/PR to main branch

**Why:** terraform-test requires a locally-built provider via dev overrides, hence needs build. It also correctly sets ANTHROPIC_API_KEY from secrets and uses terraform_wrapper: false (required for dev overrides to function correctly with the wrapper disabled).
**How to apply:** When auditing new targets, check this map first to avoid re-auditing already confirmed targets.
