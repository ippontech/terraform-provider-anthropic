// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"maps"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// protocolBlock is the value of the provider's nested block name holding set,
// every other attribute null.
func (s *protocolServer) protocolBlock(name string, set map[string]tftypes.Value) tftypes.Value {
	s.t.Helper()
	typ, ok := s.schema.Provider.ValueType().(tftypes.Object).AttributeTypes[name].(tftypes.Object)
	if !ok {
		s.t.Fatalf("provider attribute %s is not an object", name)
	}
	values := map[string]tftypes.Value{}
	for attr, attrType := range typ.AttributeTypes {
		values[attr] = tftypes.NewValue(attrType, nil)
	}
	maps.Copy(values, set)
	return tftypes.NewValue(typ, values)
}

func protocolWarnings(diags []*tfprotov6.Diagnostic) []*tfprotov6.Diagnostic {
	var out []*tfprotov6.Diagnostic
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityWarning {
			out = append(out, d)
		}
	}
	return out
}

// TestPlatformBetaWarning plays Terraform's ConfigureProvider for several
// provider aliases, each on a provider instance of its own, as Terraform runs
// them. Every alias that a platform block activates gets exactly one warning,
// on that block, saying the platform's support is in beta and linking the
// guide and the issue tracker. A first-party alias gets no warning at all.
// The base URLs are closed local ports: Configure sends nothing.
func TestPlatformBetaWarning(t *testing.T) {
	const (
		awsGuide = "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_platform_on_aws"
		gcGuide  = "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_models_on_google_cloud"
		gcBeta   = "Claude Models on Google Cloud (Anthropic managed) support is in beta"
	)

	aliases := []struct {
		name    string
		setup   func(t *testing.T)
		config  func(s *protocolServer) map[string]tftypes.Value
		block   string // the block the warning is on; empty for no warning
		summary string
		guide   string
	}{
		{
			name: "aws, signed, us-west-2",
			config: func(s *protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{
					"workspace_id": tfString(awsTestWorkspace),
					"aws": s.protocolBlock("aws", map[string]tftypes.Value{
						"region": tfString(awsTestRegion), "access_key": tfString(awsTestAccessKey),
						"secret_key": tfString(awsTestSecretKey), "base_url": tfString("http://127.0.0.1:9"),
					}),
				}
			},
			block: "aws", summary: "Claude Platform on AWS support is in beta", guide: awsGuide,
		},
		{
			name: "aws, console key, eu-west-3",
			config: func(s *protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{
					"workspace_id": tfString("wrkspc_01AwsSecondWorkspace"),
					"aws": s.protocolBlock("aws", map[string]tftypes.Value{
						"region": tfString("eu-west-3"), "api_key": tfString("aws-console-key"),
						"base_url": tfString("http://127.0.0.1:9"),
					}),
				}
			},
			block: "aws", summary: "Claude Platform on AWS support is in beta", guide: awsGuide,
		},
		{
			name: "google_cloud, with a workspace",
			config: func(s *protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{
					"workspace_id": tfString(gcTestWorkspace),
					"google_cloud": s.protocolBlock("google_cloud", map[string]tftypes.Value{
						"project": tfString(gcTestProject), "access_token": tfString(gcTestToken),
						"base_url": tfString("http://127.0.0.1:9"),
					}),
				}
			},
			block: "google_cloud", summary: gcBeta, guide: gcGuide,
		},
		{
			name: "google_cloud, organization routes only",
			config: func(s *protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{
					"google_cloud": s.protocolBlock("google_cloud", map[string]tftypes.Value{
						"project": tfString("my-other-project"), "access_token": tfString(gcTestToken),
						"base_url": tfString("http://127.0.0.1:9"),
					}),
				}
			},
			block: "google_cloud", summary: gcBeta, guide: gcGuide,
		},
		{
			name: "first party, api_key",
			config: func(*protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{"api_key": tfString("sk-ant-api03-test")}
			},
		},
		{
			name: "first party, platform variables exported",
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("ANTHROPIC_GOOGLE_CLOUD_PROJECT", gcTestProject)
				t.Setenv("ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID", gcTestWorkspace)
				t.Setenv("AWS_REGION", awsTestRegion)
				t.Setenv("AWS_ACCESS_KEY_ID", awsTestAccessKey)
				t.Setenv("AWS_SECRET_ACCESS_KEY", awsTestSecretKey)
				t.Setenv("ANTHROPIC_AWS_API_KEY", "aws-console-key")
				t.Setenv("ANTHROPIC_AWS_WORKSPACE_ID", awsTestWorkspace)
			},
			config: func(*protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{"api_key": tfString("sk-ant-api03-test")}
			},
		},
		{
			name: "first party, every credential",
			config: func(*protocolServer) map[string]tftypes.Value {
				return map[string]tftypes.Value{
					"api_key": tfString("sk-ant-api03-test"), "admin_api_key": tfString("sk-ant-admin01-test"),
					"auth_token": tfString("sk-ant-oat01-test"), "workspace_id": tfString("wrkspc_first_party"),
				}
			},
		},
	}

	warned := 0
	for _, alias := range aliases {
		t.Run(alias.name, func(t *testing.T) {
			isolateGoogleEnv(t) // the AWS isolation included
			if alias.block != "" {
				exportFirstPartyEnvironment(t)
			}
			if alias.setup != nil {
				alias.setup(t)
			}
			s := newProtocolServer(t)
			diags := s.configure(alias.config(s))
			if hasProtocolError(diags) {
				t.Fatalf("ConfigureProvider: %v", summaries(diags))
			}

			warnings := protocolWarnings(diags)
			if alias.block == "" {
				if len(warnings) != 0 {
					t.Errorf("a first-party alias got warnings: %v", summaries(warnings))
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("got %d warnings, want exactly one: %v", len(warnings), summaries(warnings))
			}
			warned++
			w := warnings[0]
			if w.Summary != alias.summary {
				t.Errorf("summary = %q, want %q", w.Summary, alias.summary)
			}
			if want := tftypes.NewAttributePath().WithAttributeName(alias.block); !w.Attribute.Equal(want) {
				t.Errorf("attribute = %v, want %v", w.Attribute, want)
			}
			for _, part := range []string{"verified with offline tests only", "report what works and what does not"} {
				if !strings.Contains(w.Detail, part) {
					t.Errorf("detail %q lacks %q", w.Detail, part)
				}
			}
			// Each link is there and ends its line: no punctuation a terminal
			// or a copy could take for part of it.
			for _, link := range []string{alias.guide, providerIssuesURL} {
				if !strings.Contains(w.Detail+"\n", link+"\n") {
					t.Errorf("detail %q lacks %q at the end of a line", w.Detail, link)
				}
			}
		})
	}
	if want := 4; warned != want {
		t.Errorf("%d aliases warned, want %d: one per platform alias", warned, want)
	}
}
