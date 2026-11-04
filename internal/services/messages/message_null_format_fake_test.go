// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// TestMessageResource_nullFormatRejected: output_config.format set to JSON
// null is rejected before any request, as anthropic_count_tokens does,
// instead of being dropped from the request, which sent the message
// without the structured output the configuration asks for. ModifyPlan
// rejects it when it plans a create, so no other resource of the apply is
// created first.
// Reference: https://platform.claude.com/docs/en/api/messages/create,
// output_config.format, a JSONOutputFormat whose schema is required.
func TestMessageResource_nullFormatRejected(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/messages", http.StatusOK, map[string]any{"id": "msg_1", "type": "message", "role": "assistant"})
	config := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_message" "test" {
  model         = "claude-opus-5-5"
  max_tokens    = 128
  messages      = [{ role = "user", content = "What is the capital of France?" }]
  output_config = { format = jsonencode(null) }
}
`
	invalid := regexp.MustCompile(`(?s)Invalid\s+JSON\s+Schema.*output_config.format\s+must\s+be\s+a\s+JSON\s+object.*got\s+null`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			{Config: config, PlanOnly: true, ExpectError: invalid},
			{Config: config, ExpectError: invalid},
		},
	})
	srv.AssertCalled("", 0)
}

// TestMessageResource_nullFormatOnReplace: changing the format of a sent
// message to null replaces it, and Terraform plans the new message as a
// create with no prior state, so the null format is rejected at plan before
// the message is sent again.
func TestMessageResource_nullFormatOnReplace(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/messages", http.StatusOK, map[string]any{"id": "msg_1", "type": "message", "role": "assistant"})
	config := func(format string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_message" "test" {
  model         = "claude-opus-5-5"
  max_tokens    = 128
  messages      = [{ role = "user", content = "What is the capital of France?" }]
  output_config = { format = jsonencode(` + format + `) }
}
`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			{Config: config(`{ type = "object" }`)},
			{
				Config:      config(`null`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+JSON\s+Schema.*got\s+null`),
			},
		},
	})
	srv.AssertCalled("POST /v1/messages", 1)
}
