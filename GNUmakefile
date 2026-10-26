default: fmt tidy-check lint test install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

# Hidden directories are skipped, as `./...` skips them for the other targets:
# agent worktrees under .claude/worktrees/ and the per-worktree .gocache/ hold
# Go files that are not this checkout's. Hidden files are skipped too, as gofmt
# itself skips them when it walks a directory (macOS `._*.go` files, say). `-exec ... {} +` hands the paths to
# gofmt unsplit (names with spaces or glob characters survive), and find exits
# non-zero when gofmt does, so a file that does not parse still fails the target.
fmt:
	find . -name '.?*' -prune -o -type f -name '*.go' -exec gofmt -s -w -e {} +

# `-diff` reports what `go mod tidy` would change and exits non-zero, without
# touching go.mod/go.sum — so the check is safe to run on a dirty worktree and
# needs no `git diff --exit-code` follow-up (Go 1.23+).
tidy-check:
	@go mod tidy -diff || \
		(echo; echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result."; exit 1)

# COVERPROFILE=<file> also writes a coverage profile (the CI test job does).
test:
	go test -v -cover$(if $(COVERPROFILE), -coverprofile=$(COVERPROFILE)) -timeout=120s -parallel=10 ./...

# Unit tests, then the coverage gate of the CI coverage job (.coverage.json,
# tools/coversummary): the diff rule against COVER_BASE (committed changes
# only), the ratchet and the floor, followed by the coverage of each resource
# and data source file. Fails like the CI job does.
COVER_BASE ?= origin/main
cover: COVERPROFILE := coverage.out
cover: test
	git diff --unified=0 --no-color $(COVER_BASE)...HEAD > coverage-diff.out
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
