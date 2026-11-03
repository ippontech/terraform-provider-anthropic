// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// defaultBaseURL is the host the routine fire endpoint lives on. It is
	// the Claude Platform host, but the endpoint belongs to Claude Code: it
	// takes a per-routine bearer token, not an API key.
	defaultBaseURL = "https://api.anthropic.com"

	// routineFireTimeout bounds one fire request. The endpoint returns as
	// soon as the session is created; it does not wait for the run.
	routineFireTimeout = 60 * time.Second

	// maxFireTextLength is the API's limit on `text`, in characters.
	maxFireTextLength = 65536
)

// routineIDPattern matches a routine ID. Despite the path parameter's name,
// routine IDs are prefixed `trig_`, not `routine_`.
var routineIDPattern = regexp.MustCompile(`^trig_[A-Za-z0-9]+$`)

var _ action.Action = &RoutineFireAction{}

func NewRoutineFireAction() action.Action {
	return &RoutineFireAction{}
}

// RoutineFireAction starts a run of an existing Claude Code routine through
// its API trigger (`POST /v1/claude_code/routines/{routine_id}/fire`).
//
// It uses none of the provider's credentials: the endpoint authenticates with
// the routine's own bearer token, so the action carries its own HTTP client
// instead of one of the SDK clients in providerdata.
type RoutineFireAction struct {
	// httpClient and baseURL are fields so that the unit tests can point the
	// action at an httptest server; nil and "" mean the defaults.
	httpClient *http.Client
	baseURL    string
}

type RoutineFireActionModel struct {
	RoutineID types.String `tfsdk:"routine_id"`
	Token     types.String `tfsdk:"token"`
	Text      types.String `tfsdk:"text"`
}

// routineFireRequest is the body of the fire request.
type routineFireRequest struct {
	Text string `json:"text,omitempty"`
}

// routineFireResponse is the body of a successful fire request.
type routineFireResponse struct {
	Type       string `json:"type"`
	SessionID  string `json:"claude_code_session_id"`
	SessionURL string `json:"claude_code_session_url"`
}

// routineFireAPIError is an error answer of the fire endpoint, decoded from
// the standard Anthropic error envelope where the body carries one.
type routineFireAPIError struct {
	StatusCode int
	Type       string
	Message    string
	RetryAfter string
	RequestID  string
}

func (e *routineFireAPIError) Error() string {
	msg := fmt.Sprintf("HTTP %d", e.StatusCode)
	if e.Type != "" {
		msg += " " + e.Type
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

func (a *RoutineFireAction) Metadata(ctx context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_code_routine_fire"
}

func (a *RoutineFireAction) Schema(ctx context.Context, req action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Starts a run of an existing Claude Code routine through its API trigger. " +
			"Each invocation creates a new Claude Code session; the endpoint has no idempotency key, so the action never retries a fire.",
		Attributes: map[string]schema.Attribute{
			"routine_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The routine's ID, prefixed `trig_`. It is part of the URL the routine's API trigger dialog shows " +
					"(`https://api.anthropic.com/v1/claude_code/routines/<routine_id>/fire`).",
				Validators: []validator.String{
					stringvalidator.RegexMatches(routineIDPattern, "must be a routine ID starting with `trig_`"),
				},
			},
			"token": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The routine's API trigger bearer token (`sk-ant-oat01-...`), generated in the routine's API trigger dialog " +
					"at claude.ai/code/routines. It is scoped to this routine and shown only once. " +
					"Action configuration is never stored in state, but action schemas cannot mark an attribute sensitive, " +
					"so pass the token through a `sensitive` variable to keep it out of plan output. " +
					"Ephemeral values are not accepted: Terraform allows them only in write-only attributes, which actions do not support.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"text": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Run-specific context passed to the routine alongside its saved prompt, such as an alert body or a git diff. " +
					"Freeform and not parsed. The routine receives it wrapped as untrusted data, so its saved prompt must say what to do with it. " +
					"At most 65,536 characters.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtMost(maxFireTextLength),
				},
			},
		},
	}
}

func (a *RoutineFireAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data RoutineFireActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routineID := data.RoutineID.ValueString()

	fired, err := fireRoutine(ctx, a.client(), a.resolveBaseURL(), routineID, data.Token.ValueString(), data.Text.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("routine_id"), "Failed to Fire Claude Code Routine", fireErrorDetail(routineID, err))
		return
	}

	tflog.Info(ctx, "Fired Claude Code routine", map[string]any{
		"routine_id": routineID,
		"session_id": fired.SessionID,
	})
	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("Fired routine %s: session %s (%s)", routineID, fired.SessionID, fired.SessionURL),
	})
}

func (a *RoutineFireAction) client() *http.Client {
	if a.httpClient != nil {
		return a.httpClient
	}
	return &http.Client{Timeout: routineFireTimeout}
}

// resolveBaseURL honours ANTHROPIC_BASE_URL the same way the provider's SDK
// clients do: an exported-but-empty value keeps the production default.
func (a *RoutineFireAction) resolveBaseURL() string {
	if a.baseURL != "" {
		return a.baseURL
	}
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		return baseURL
	}
	return defaultBaseURL
}

// fireRoutine sends one fire request and decodes the answer. It never retries:
// every successful request creates a new session, and the endpoint has no
// idempotency key, so a retry after a dropped connection could start the
// routine twice.
func fireRoutine(ctx context.Context, client *http.Client, baseURL, routineID, token, text string) (*routineFireResponse, error) {
	body, err := json.Marshal(routineFireRequest{Text: text})
	if err != nil {
		return nil, fmt.Errorf("encoding request body: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/claude_code/routines/" + routineID + "/fire"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, decodeRoutineFireError(httpResp, respBody)
	}

	var fired routineFireResponse
	if err := json.Unmarshal(respBody, &fired); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if fired.Type != "routine_fire" || fired.SessionID == "" {
		return nil, fmt.Errorf("unexpected response (type %q, session ID %q); the endpoint is experimental and its response shape may have changed",
			fired.Type, fired.SessionID)
	}

	return &fired, nil
}

func decodeRoutineFireError(httpResp *http.Response, body []byte) error {
	apiErr := &routineFireAPIError{
		StatusCode: httpResp.StatusCode,
		RetryAfter: httpResp.Header.Get("Retry-After"),
		RequestID:  httpResp.Header.Get("request-id"),
	}

	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		apiErr.Type = envelope.Error.Type
		apiErr.Message = envelope.Error.Message
	}

	return apiErr
}

// fireErrorDetail turns a fire error into a diagnostic detail that says what
// to do about it. The token never appears in it.
func fireErrorDetail(routineID string, err error) string {
	var apiErr *routineFireAPIError
	if !errors.As(err, &apiErr) {
		return fmt.Sprintf("Could not fire routine %s: %s. The routine may or may not have started; check its runs at https://claude.ai/code/routines before invoking the action again.",
			routineID, err)
	}

	detail := fmt.Sprintf("Could not fire routine %s: %s.", routineID, apiErr)

	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		detail += " Check that the routine is not paused and that `text` is at most 65,536 characters."
	case http.StatusUnauthorized:
		detail += " The token is missing or does not belong to this routine. Generating a new token in the routine's API trigger dialog revokes the previous one."
	case http.StatusForbidden:
		detail += " The account or organization has no access to routines: they need a Pro, Max, Team or Enterprise plan with Claude Code on the web enabled, and an Owner can turn routines off for a Team or Enterprise organization."
	case http.StatusNotFound:
		detail += " No routine has this ID."
	case http.StatusTooManyRequests:
		detail += " An hourly fire limit was reached (30 per routine, 100 per account)."
		if apiErr.RetryAfter != "" {
			detail += fmt.Sprintf(" Retry-After: %s.", apiErr.RetryAfter)
		}
	default:
		if apiErr.StatusCode >= http.StatusInternalServerError {
			detail += " The routine may or may not have started; check its runs at https://claude.ai/code/routines before invoking the action again."
		}
	}

	if apiErr.RequestID != "" {
		detail += fmt.Sprintf(" Request ID: %s.", apiErr.RequestID)
	}

	return detail
}
