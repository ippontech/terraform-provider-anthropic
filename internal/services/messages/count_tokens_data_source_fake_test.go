// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
)

const countTokensAddr = "data.anthropic_count_tokens.test"

func countTokensConfig(attributes string) string {
	return config(`
data "anthropic_count_tokens" "test" {
  model    = "claude-opus-5-5"
  messages = [{ role = "user", content = "How many tokens is this?" }]
` + attributes + `}
`)
}

const minimalCountBody = `{"model": "claude-opus-5-5",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "How many tokens is this?"}]}]}`

// TestCountTokensDataSource_read counts a minimal prompt, then one with
// every option, through Terraform: the request bodies are exact (system as a
// string, the format wrapped as a json_schema output format, an empty
// cache_control still sent) and input_tokens is the API's count.
func TestCountTokensDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newMessagesFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{
				Config: countTokensConfig(""),
				ConfigStateChecks: expect(countTokensAddr, map[string]knownvalue.Check{
					"input_tokens":  knownvalue.Int64Exact(5),
					"system":        knownvalue.Null(),
					"thinking":      knownvalue.Null(),
					"output_config": knownvalue.Null(),
					"cache_control": knownvalue.Null(),
				}),
			},
			{
				Config: countTokensConfig(`
  system        = "You are a counting assistant."
  speed         = "fast"
  thinking      = { type = "enabled", budget_tokens = 1024, display = "omitted" }
  output_config = { effort = "max", format = jsonencode({ type = "object" }) }
  cache_control = {}
`),
				ConfigStateChecks: expect(countTokensAddr, map[string]knownvalue.Check{
					"input_tokens": knownvalue.Int64Exact(10),
					"system":       knownvalue.StringExact("You are a counting assistant."),
					"thinking": knownvalue.ObjectExact(map[string]knownvalue.Check{
						"type": knownvalue.StringExact("enabled"), "budget_tokens": knownvalue.Int64Exact(1024), "display": knownvalue.StringExact("omitted"),
					}),
				}),
			},
		},
	})
	// Each step reads the data source three times: in the plan, in the
	// apply's plan and in the refresh that follows.
	const fullCountBody = `{
  "model": "claude-opus-5-5",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "How many tokens is this?"}]}],
  "system": "You are a counting assistant.",
  "speed": "fast",
  "thinking": {"type": "enabled", "budget_tokens": 1024, "display": "omitted"},
  "output_config": {"effort": "max", "format": {"type": "json_schema", "schema": {"type": "object"}}},
  "cache_control": {"type": "ephemeral"}
}`
	srv.AssertJSONBodies(routeCountTokens, minimalCountBody, minimalCountBody, minimalCountBody, fullCountBody, fullCountBody, fullCountBody)
	srv.assertRequestShape(routeCountTokens)
}

// TestCountTokensDataSource_errors: an API error and a format that is not a
// JSON object are error diagnostics, the latter before any request; the
// thinking rules are checked before the read.
func TestCountTokensDataSource_errors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		attributes, want string
		fail             bool
		calls            int
	}{
		"API error": {
			fail: true, calls: 1,
			want: `(?s)Unable\s+to\s+count\s+tokens:.*400\s+Bad\s+Request`,
		},
		"format null": {
			attributes: `  output_config = { format = jsonencode(null) }` + "\n",
			want:       `(?s)Invalid\s+format.*must\s+be\s+a\s+JSON\s+object`,
		},
		"format not an object": {
			attributes: `  output_config = { format = jsonencode([1]) }` + "\n",
			want:       `(?s)Invalid\s+format.*must\s+be\s+a\s+JSON\s+object`,
		},
		"budget missing": {
			attributes: `  thinking = { type = "enabled" }` + "\n",
			want:       "(?s)Missing\\s+budget_tokens.*`budget_tokens`\\s+is\\s+required",
		},
		"display with between_tools": {
			attributes: `  thinking = { type = "between_tools", display = "summarized" }` + "\n",
			want:       "(?s)Invalid\\s+display.*not\\s+allowed\\s+when\\s+`thinking.type`\\s+is\\s+\"between_tools\"",
		},
		"budget with adaptive": {
			attributes: `  thinking = { type = "adaptive", budget_tokens = 2048 }` + "\n",
			want:       "(?s)Invalid\\s+budget_tokens.*only\\s+allowed\\s+when",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newMessagesFake(t)
			if tc.fail {
				srv.FailNext(routeCountTokens, http.StatusBadRequest, 1)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config:      countTokensConfig(tc.attributes),
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
			srv.AssertCalled("", tc.calls)
		})
	}
}
