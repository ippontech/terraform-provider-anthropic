default: fmt tidy-check lint complexity test install generate

build:
	go build -v ./...

install: build
	go install -v ./...

# .golangci.yml reports only code that differs from origin/main; the second
# run keeps the standard linters on the whole tree.
lint:
	golangci-lint run
	golangci-lint run --no-config

# The complexity ratchet of the CI complexity job (tools/complexity): every
# function in the non-test Go files under internal/ scores 20 or less, unless
# it already scored more at the merge base with COMPLEXITY_BASE and its score
# did not rise. Reads the working tree, committed or not.
COMPLEXITY_BASE ?= origin/main
complexity:
	@go run -C tools ./complexity -base $(COMPLEXITY_BASE)

.PHONY: complexity

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

# `-diff` reports what `go mod tidy` would change and exits non-zero, without
# touching go.mod/go.sum — so the check is safe to run on a dirty worktree and
# needs no `git diff --exit-code` follow-up (Go 1.23+).
tidy-check:
	@go mod tidy -diff || \
		(echo; echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result."; exit 1)

# COVERPROFILE=<file> also writes a coverage profile (the CI test job does).
test:
	go test -v -cover$(if $(COVERPROFILE), -coverprofile=$(COVERPROFILE)) -timeout=300s -parallel=10 ./...

# Unit tests, then the coverage gate of the CI coverage job (.coverage.json,
# tools/coversummary): the diff rule against the merge base with COVER_BASE,
# the ratchet and the floor, followed by the coverage of each resource and
# data source file. Fails like the CI job does. The tests profile the working
# tree, so the diff goes from the merge base to the working tree too:
# committed and uncommitted changes to tracked files (git add -N a new file).
# The git diff options pin the format the gate parses, whatever the local git
# configuration says.
COVER_BASE ?= origin/main
cover: COVERPROFILE := coverage.out
cover: test
	base=$$(git merge-base $(COVER_BASE) HEAD) && \
		git diff --no-ext-diff --src-prefix=a/ --dst-prefix=b/ --unified=0 --no-color "$$base" > coverage-diff.out
	go run -C tools ./coversummary -config $(abspath .coverage.json) -diff $(abspath coverage-diff.out) $(abspath $(COVERPROFILE))

.PHONY: cover

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.dev.tfrc:
	@GOBIN=$$(go env GOBIN); \
	printf 'provider_installation {\n  dev_overrides {\n    "registry.terraform.io/ippontech/anthropic" = "%s"\n  }\n  direct {}\n}\n' \
		"$${GOBIN:-$$(go env GOPATH)/bin}" > $@

# tests/ is the root module: it holds versions.tf (the provider source mapping
# the test files' provider/mock_provider blocks resolve against) so the
# repository root stays free of Terraform configuration.
terraform-test: install .dev.tfrc
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests init
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests test

# Offline Terraform checks, run on every pull request (terraform.yml): no
# credentials, no API call, no registry download (the provider comes from
# dev_overrides, so neither validate nor init installs it).
EXAMPLE_DIRS := $(sort $(dir $(wildcard examples/*/*.tf examples/*/*/*.tf)))
# A test file with a mock_provider block runs on every pull request, so it must
# not need credentials.
MOCK_TESTS := $(notdir $(shell grep -l '^mock_provider' tests/*.tftest.hcl))

terraform-fmt-check:
	terraform fmt -check -diff -recursive

# Each example directory against the provider just built: catches an attribute
# renamed or removed in the schema that an example (and so the docs) still uses.
terraform-validate: install .dev.tfrc
	@failed=0; for dir in $(EXAMPLE_DIRS); do \
		if out=$$(TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=$$dir validate -no-color 2>&1); then \
			echo "valid:   $$dir"; \
		else \
			echo "INVALID: $$dir"; echo "$$out"; failed=1; \
		fi; \
	done; exit $$failed

# Without any -filter, terraform test would run every test file, live ones
# included: an empty list is an error.
terraform-test-mock: install .dev.tfrc
	$(if $(MOCK_TESTS),,$(error no test file in tests/ has a mock_provider block))
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests init
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests test $(addprefix -filter=,$(MOCK_TESTS))

.PHONY: fmt tidy-check lint test testacc terraform-test terraform-fmt-check terraform-validate terraform-test-mock build install generate
