default: fmt tidy-check lint test install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

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

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

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
