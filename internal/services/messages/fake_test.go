// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Messages API the package uses (standard key, no beta).
// References: https://platform.claude.com/docs/en/api/messages/create and
// https://platform.claude.com/docs/en/api/messages/count_tokens.
const (
	routeCreateMessage = "POST /v1/messages"
	routeCountTokens   = "POST /v1/messages/count_tokens"
)

// Values the fake reports for what the request leaves to the API. The
// create reference's response example reports "service_tier": "standard",
// "inference_geo": "global" and "speed": "standard".
const (
	servedTier  = "standard"
	servedGeo   = "global"
	servedSpeed = "standard"
	// thinkingTokens is what a request with thinking enabled or adaptive
	// spends on reasoning; outputTokens is the visible answer.
	thinkingTokens = 30
	outputTokens   = 5
)

// messagesFake answers the two Messages endpoints from the request alone:
// the Messages API stores nothing a client could read back.
type messagesFake struct {
	*sdktest.Server

	mu      sync.Mutex
	created map[string]bool
	refuse  bool
}

func newMessagesFake(t *testing.T) *messagesFake {
	t.Helper()
	f := &messagesFake{Server: sdktest.NewServer(t), created: map[string]bool{}}
	f.Handle(routeCreateMessage, f.createMessage)
	f.Handle(routeCountTokens, f.countTokens)
	return f
}

// refuseNext makes the next messages end in a refusal.
func (f *messagesFake) refuseNext() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = true
}

// messageRequest is the part of a create or count_tokens body the fake
// reads. system is a string on count_tokens and a list of text blocks on
// create; both references accept either.
type messageRequest struct {
	Model    string `json:"model"`
	System   any    `json:"system"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Text         string          `json:"text"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"content"`
	} `json:"messages"`
	StopSequences []string        `json:"stop_sequences"`
	InferenceGeo  string          `json:"inference_geo"`
	Speed         string          `json:"speed"`
	CacheControl  json.RawMessage `json:"cache_control"`
	Thinking      *struct {
		Type string `json:"type"`
	} `json:"thinking"`
	Diagnostics *struct {
		PreviousMessageID *string `json:"previous_message_id"`
	} `json:"diagnostics"`
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (messageRequest, bool) {
	var req messageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return req, false
	}
	return req, true
}

// inputTokens counts one token per word of the system prompt and the
// messages, so a message and the count of the same prompt agree.
func (req messageRequest) inputTokens() int {
	n := 0
	switch s := req.System.(type) {
	case string:
		n += len(strings.Fields(s))
	case []any:
		for _, b := range s {
			if block, ok := b.(map[string]any); ok {
				text, _ := block["text"].(string)
				n += len(strings.Fields(text))
			}
		}
	}
	for _, m := range req.Messages {
		for _, c := range m.Content {
			n += len(strings.Fields(c.Text))
		}
	}
	return n
}

// cached reports whether the request carries a cache_control breakpoint,
// top-level or on a message block.
func (req messageRequest) cached() bool {
	if len(req.CacheControl) > 0 {
		return true
	}
	for _, m := range req.Messages {
		for _, c := range m.Content {
			if len(c.CacheControl) > 0 {
				return true
			}
		}
	}
	return false
}

// createMessage answers POST /v1/messages with the response object of the
// reference ("Returns"):
//   - content: a thinking block first when thinking is enabled or adaptive,
//     then two text blocks, "Echo: " and the last message's text;
//   - stop_reason "stop_sequence" with stop_sequence set to the first of the
//     request's stop_sequences, otherwise "end_turn" with stop_sequence null;
//     after refuseNext, "refusal" with stop_details {type, category,
//     explanation} as in the reference's example, otherwise stop_details
//     null ("null when the stop_reason has no additional detail");
//   - usage: input_tokens (see inputTokens), every input token as
//     cache_creation_input_tokens when the request has a cache_control
//     breakpoint, output_tokens_details.thinking_tokens, and the
//     service_tier, inference_geo and speed the request was served with;
//   - diagnostics: null when the request did not supply diagnostics, or
//     supplied it without previous_message_id (no comparison, so no
//     divergence); with the ID of a message this fake created,
//     cache_miss_reason messages_changed with cache_missed_input_tokens;
//     with any other ID, previous_message_not_found, which carries no token
//     count.
//
// The API stores nothing, so the answer derives from the request: every
// field above is one mapMessageToState maps into state, and the two shapes
// of cache_miss_reason are the ones cacheMissReasonValue tells apart
// (https://platform.claude.com/docs/en/api/messages/create).
func (f *messagesFake) createMessage(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeRequest(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	id := fmt.Sprintf("msg_%024d", len(f.created)+1)
	f.created[id] = true
	refuse := f.refuse
	f.refuse = false
	var reason any
	if d := req.Diagnostics; d != nil && d.PreviousMessageID != nil {
		reason = map[string]any{"type": "previous_message_not_found"}
		if f.created[*d.PreviousMessageID] {
			reason = map[string]any{"type": "messages_changed", "cache_missed_input_tokens": req.inputTokens()}
		}
	}
	f.mu.Unlock()

	var content []any
	thinking := 0
	if req.Thinking != nil && (req.Thinking.Type == "enabled" || req.Thinking.Type == "adaptive") {
		thinking = thinkingTokens
		content = append(content, map[string]any{"type": "thinking", "thinking": "Let me think.", "signature": "sig"})
	}
	last := req.Messages[len(req.Messages)-1].Content[0].Text
	content = append(content, map[string]any{"type": "text", "text": "Echo: "}, map[string]any{"type": "text", "text": last})

	msg := map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": req.Model, "content": content,
		"stop_reason": "end_turn", "stop_sequence": nil, "stop_details": nil, "diagnostics": nil,
	}
	if len(req.StopSequences) > 0 {
		msg["stop_reason"], msg["stop_sequence"] = "stop_sequence", req.StopSequences[0]
	}
	if refuse {
		msg["stop_reason"] = "refusal"
		msg["stop_details"] = map[string]any{
			"type": "refusal", "category": "cyber",
			"explanation": "This request was declined because it conflicts with Anthropic's Usage Policy.",
		}
	}
	if reason != nil {
		msg["diagnostics"] = map[string]any{"cache_miss_reason": reason}
	}
	created := 0
	if req.cached() {
		created = req.inputTokens()
	}
	msg["usage"] = map[string]any{
		"input_tokens": req.inputTokens(), "output_tokens": outputTokens + thinking,
		"output_tokens_details":       map[string]any{"thinking_tokens": thinking},
		"cache_creation_input_tokens": created, "cache_read_input_tokens": 0,
		"service_tier": servedTier, "inference_geo": orDefault(req.InferenceGeo, servedGeo), "speed": orDefault(req.Speed, servedSpeed),
	}
	sdktest.WriteJSON(w, http.StatusOK, msg)
}

// countTokens answers POST /v1/messages/count_tokens with the reference's
// MessageTokensCount object, {"input_tokens": N}, counted as createMessage
// counts, which CountTokensDataSource.Read maps
// (https://platform.claude.com/docs/en/api/messages/count_tokens).
func (f *messagesFake) countTokens(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeRequest(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"input_tokens": req.inputTokens()})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// assertRequestShape checks that every request used the standard API key
// and no beta header (the count_tokens note in this package's CLAUDE.md).
func (f *messagesFake) assertRequestShape(routes ...string) {
	for _, route := range routes {
		f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey}, route)
	}
}

func factories(f *messagesFake) map[string]func() (tfprotov6.ProviderServer, error) {
	return providertest.ProviderFactories(f.Server)
}

func config(blocks string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + blocks
}

// messageID is the ID the fake gives to the n-th message it creates.
func messageID(n int) string { return fmt.Sprintf("msg_%024d", n) }

// expect checks attributes of addr in state.
func expect(addr string, values map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(values))
	for name, v := range values {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), v))
	}
	return checks
}

// expectAction checks the planned action of anthropic_message.test and an
// empty plan after the apply and a refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(messageAddr, action)
}
