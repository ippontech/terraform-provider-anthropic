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

.PHONY: fmt tidy-check lint test testacc terraform-test build install generate
