---
name: CI workflow coverage map
description: Full mapping of GNUmakefile targets to GitHub Actions jobs, confirmed coverage status per workflow file
type: project
---

Makefile target to CI job mapping for terraform-provider-anthropic (last audited 2026-04-13):

Workflow files in .github/workflows/:
- provider.yml  — triggers on: pull_request; jobs: lint, test, generate
- testacc.yml   — triggers on: push to main + workflow_dispatch; jobs: testacc, terraform-test
- goreleaser-snapshot.yml — triggers on: pull_request; job: goreleaser-snapshot (cross-compilation build check)
- goreleaser-release.yml  — triggers on: push tag v*.*.*; job: goreleaser-release
- semantic-release.yml    — triggers on: push to main; job: release (semantic-release)

| Makefile Target  | What It Does                              | CI Job / Workflow File                   | Covered? |
|------------------|-------------------------------------------|------------------------------------------|----------|
| make (default)   | fmt + lint + install + generate           | N/A (dev convenience)                    | Intentionally excluded |
| make build       | go build                                  | test job in provider.yml (make build step) | Yes |
| make install     | go install to GOPATH/bin                  | terraform-test job in testacc.yml (via make terraform-test which calls install) | Yes |
| make fmt         | gofmt formatting                          | lint job in provider.yml (inline gofmt -l check + golangci-lint) | Yes |
| make lint        | golangci-lint                             | lint job in provider.yml                 | Yes |
| make test        | unit tests, 120s timeout, 10 parallel     | test job in provider.yml                 | Yes |
| make testacc     | acceptance tests (TF_ACC=1, 120m timeout) | testacc job in testacc.yml               | Yes — runs on push to main only |
| make generate    | regenerate docs, format examples          | generate job in provider.yml (runs make generate + checks for uncommitted diff) | Yes |
| make terraform-test | terraform init + terraform test via dev overrides | terraform-test job in testacc.yml | Yes — runs on push to main only |

Notes on testacc/terraform-test trigger gap:
- Both testacc and terraform-test jobs run only on push to main (or workflow_dispatch), NOT on PRs.
- This means acceptance tests do not gate pull requests — by design (requires real ANTHROPIC_API_KEY in the acctest environment).

Known coverage gap as of 2026-04-13:
- New anthropic_message resource has two acceptance test functions (TestAccMessageResource, TestAccMessageResourceWithSystemAndTemperature) in message_resource_test.go that will be picked up by make testacc — no CI change needed for those.
- There is NO .tftest.hcl file for the message resource in tests/ — only tests/models.tftest.hcl and tests/provider.tftest.hcl exist. The terraform-test job will NOT exercise the message resource via terraform test.

**Why:** terraform test relies on .tftest.hcl files under tests/. The message resource example exists at examples/resources/message/resource.tf but has no corresponding .tftest.hcl test file.
**How to apply:** When auditing new resources, check both internal/provider/*_test.go (for make testacc coverage) AND tests/*.tftest.hcl (for make terraform-test coverage).
