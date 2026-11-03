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

.PHONY: fmt tidy-check lint test testacc terraform-test build install generate
