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
// without the structured output the configuration asks for.
// Reference: https://platform.claude.com/docs/en/api/messages/create,
// output_config.format, a JSONOutputFormat whose schema is required.
func TestMessageResource_nullFormatRejected(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/messages", http.StatusOK, map[string]any{"id": "msg_1", "type": "message", "role": "assistant"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_message" "test" {
  model         = "claude-opus-5-5"
  max_tokens    = 128
  messages      = [{ role = "user", content = "What is the capital of France?" }]
  output_config = { format = jsonencode(null) }
}
`,
			ExpectError: regexp.MustCompile(`(?s)Invalid\s+JSON\s+Schema.*output_config.format\s+must\s+be\s+a\s+JSON\s+object.*got\s+null`),
		}},
	})
	srv.AssertCalled("", 0)
}
