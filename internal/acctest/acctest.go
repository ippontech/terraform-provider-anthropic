// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package acctest

import (
	"os"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/provider"
)

// ProtoV6ProviderFactories is used to instantiate a provider during acceptance testing.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"anthropic": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// TerraformTestsWorkspaceID is the ID of the dedicated "terraform-tests"
// workspace used to isolate acceptance-test resources from production
// workspaces. The standard ANTHROPIC_API_KEY used to run the tests is scoped to
// this workspace, so standard-API resources created during tests (vaults,
// agents, environments, skills, ...) land here automatically. Admin API data
// source tests, which are organization-wide, target this workspace by ID.
const TerraformTestsWorkspaceID = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"

// TerraformTests2WorkspaceID is the ID of the second dedicated test workspace,
// "terraform-tests-2". It exists only so the multi-workspace key (see
// PreCheckMultiWorkspace) has two workspaces to be routed between, and holds
// nothing else: tests that do not exercise the workspace_id provider argument
// must keep using TerraformTestsWorkspaceID.
const TerraformTests2WorkspaceID = "wrkspc_01MdoKe74QuV4kFCC23VKZfY"

// MultiWorkspaceAPIKeyEnv names the variable carrying the multi-workspace key
// used by the workspace_id provider-argument tests: a service account key of
// the terraform-provider-anthropic-tests account, member of terraform-tests
// and terraform-tests-2 and scoped to neither, so the API rejects its requests
// with a 400 until the anthropic-workspace-id header names one of them. Kept
// apart from ANTHROPIC_API_KEY on purpose: every other acceptance test relies
// on that key being single-workspace so its objects land in terraform-tests
// without any header.
const MultiWorkspaceAPIKeyEnv = "ANTHROPIC_MULTI_WORKSPACE_API_KEY"

// SkipUnlessAcc skips the test unless TF_ACC is set. resource.Test applies
// the same gate, but fixture setup that runs before it (seeding objects
// through the SDK) does not, so every PreCheck* helper calls this first:
// otherwise a plain `make test` with live credentials in the environment
// creates real objects, which for the WIF endpoints means archive-only
// objects in a production organization.
func SkipUnlessAcc(t *testing.T) {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance test skipped unless %s is set", resource.EnvTfAcc)
	}
}

func PreCheck(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if v := os.Getenv("ANTHROPIC_API_KEY"); v == "" {
		t.Fatal("ANTHROPIC_API_KEY must be set for acceptance tests")
	}
}

// PreCheckAdmin is used by acceptance tests that hit the Admin API
// (organization endpoints under /v1/organizations/*).
func PreCheckAdmin(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if v := os.Getenv("ANTHROPIC_ADMIN_API_KEY"); v == "" {
		t.Fatal("ANTHROPIC_ADMIN_API_KEY must be set for admin acceptance tests")
	}
}

// PreCheckOAuth is used by acceptance tests that hit endpoints requiring an
// org:admin OAuth bearer token (e.g. the Workload Identity Federation admin
// endpoints), which reject API keys outright. No test org exists yet for these
// writes and CI has no durable org:admin token, so tests gated on this run
// locally only.
func PreCheckOAuth(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if v := os.Getenv("ANTHROPIC_AUTH_TOKEN"); v == "" {
		t.Skip("ANTHROPIC_AUTH_TOKEN must be set for OAuth acceptance tests; skipping")
	}
}

// PreCheckMultiWorkspace gates the acceptance tests of the workspace_id
// provider argument, which need the multi-workspace key named by
// MultiWorkspaceAPIKeyEnv. Unlike PreCheck it skips rather than fails when
// the variable is unset: the key is a CI secret contributors do not have,
// and the tests it gates are the only ones that need it.
func PreCheckMultiWorkspace(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if v := os.Getenv(MultiWorkspaceAPIKeyEnv); v == "" {
		t.Skipf("%s must be set for multi-workspace acceptance tests; skipping", MultiWorkspaceAPIKeyEnv)
	}
}

// NewMultiWorkspaceClient returns an SDK client authenticated with the
// multi-workspace key and routed to workspaceID through the
// anthropic-workspace-id header, for tests that check out of band which
// workspace an object landed in. Call PreCheckMultiWorkspace first. Like the
// other constructors it opts out of the SDK's environment defaults.
func NewMultiWorkspaceClient(workspaceID string) *anthropic.Client {
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(os.Getenv(MultiWorkspaceAPIKeyEnv)),
		option.WithHeader("anthropic-workspace-id", workspaceID),
	)
	return &c
}

// NewOAuthClient returns an SDK client authenticated with the org:admin OAuth
// bearer token from ANTHROPIC_AUTH_TOKEN, for acceptance tests that seed
// fixtures or verify destroy/archive behaviour directly against the live WIF
// endpoints. Call PreCheckOAuth first: with the variable unset the client
// carries an empty bearer and every request is rejected. Like the provider's
// own clients it opts out of the SDK's environment defaults, so an exported
// ANTHROPIC_API_KEY (always present in a test run) is not sent alongside the
// bearer, which these endpoints reject.
func NewOAuthClient() *anthropic.Client {
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")),
	)
	return &c
}

// NewAPIKeyClient returns an SDK client authenticated with the standard API
// key from ANTHROPIC_API_KEY, for acceptance tests that seed fixtures or
// verify destroy/archive behaviour directly against the live API. Call
// PreCheck first. Like NewOAuthClient it opts out of the SDK's environment
// defaults, so an exported ANTHROPIC_AUTH_TOKEN is not sent alongside the
// key.
func NewAPIKeyClient() *anthropic.Client {
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
	)
	return &c
}
