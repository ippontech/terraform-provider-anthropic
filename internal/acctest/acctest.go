// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package acctest

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
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

func PreCheck(t *testing.T) {
	t.Helper()
	if v := os.Getenv("ANTHROPIC_API_KEY"); v == "" {
		t.Fatal("ANTHROPIC_API_KEY must be set for acceptance tests")
	}
}

// PreCheckAdmin is used by acceptance tests that hit the Admin API
// (organization endpoints under /v1/organizations/*).
func PreCheckAdmin(t *testing.T) {
	t.Helper()
	if v := os.Getenv("ANTHROPIC_ADMIN_API_KEY"); v == "" {
		t.Fatal("ANTHROPIC_ADMIN_API_KEY must be set for admin acceptance tests")
	}
}
