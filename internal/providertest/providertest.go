// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package providertest runs the provider through Terraform against an
// sdktest fake: resource.UnitTest with ProviderFactories(srv) and a
// configuration that starts with ProviderConfig. No TF_ACC, no credential and
// no network beyond the fake; the Terraform CLI must be on PATH (CI installs
// it with setup-terraform).
//
// It imports internal/provider, which imports every service package, so only
// external test packages (package <service>_test) can use it, like
// internal/acctest (CLAUDE.md, "Go tests").
//
//	func TestSkillResource_lifecycle(t *testing.T) {
//		t.Parallel()
//		srv := sdktest.NewServer(t)
//		// routes ...
//		resource.UnitTest(t, resource.TestCase{
//			ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
//			Steps: []resource.TestStep{{
//				Config: providertest.ProviderConfig(sdktest.APIKey) + `resource "anthropic_skill" "test" { ... }`,
//			}},
//		})
//		srv.AssertCalled("POST /v1/skills", 1)
//	}
package providertest

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/ippontech/terraform-provider-anthropic/internal/provider"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// ProviderFactories returns the ProtoV6ProviderFactories of a provider whose
// every client (standard, Admin, OAuth and the federation exchange) talks to
// srv, whatever ANTHROPIC_BASE_URL says. It changes no process state, so
// tests that use it can call t.Parallel.
func ProviderFactories(srv *sdktest.Server) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"anthropic": providerserver.NewProtocol6WithError(provider.New("test", provider.WithBaseURL(srv.URL))()),
	}
}

// ProviderConfig returns a provider block that sets each credential in creds
// to its sdktest dummy value and every other credential to "", so that an
// exported ANTHROPIC_API_KEY, ANTHROPIC_ADMIN_API_KEY or ANTHROPIC_AUTH_TOKEN
// cannot fill it: the provider builds exactly the clients the test asks for,
// and sdktest.Server.AssertCredential can check which one a resource used.
func ProviderConfig(creds ...sdktest.Credential) string {
	values := map[string]string{"api_key": "", "admin_api_key": "", "auth_token": ""}
	for _, c := range creds {
		switch c {
		case sdktest.APIKey:
			values["api_key"] = sdktest.TestAPIKey
		case sdktest.AdminKey:
			values["admin_api_key"] = sdktest.TestAdminKey
		case sdktest.AuthToken:
			values["auth_token"] = sdktest.TestAuthToken
		default:
			panic(fmt.Sprintf("providertest: unknown credential %v", c))
		}
	}
	var b strings.Builder
	b.WriteString("provider \"anthropic\" {\n")
	for _, name := range []string{"api_key", "admin_api_key", "auth_token"} {
		fmt.Fprintf(&b, "  %-13s = %q\n", name, values[name])
	}
	b.WriteString("}\n")
	return b.String()
}
