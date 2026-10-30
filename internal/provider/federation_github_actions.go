// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// githubActionsDefaultAudience is the audience requested when
// github_actions.audience is unset: the `aud` a federation rule expects when
// its match.audience is unset, as the Console wizard leaves it. GitHub's own
// default, the URL of the repository owner, would match only a rule whose
// match.audience names it.
const githubActionsDefaultAudience = "https://api.anthropic.com"

// githubActionsIdentityTokenTimeout bounds one request to GitHub's OIDC
// endpoint, as identityTokenCommandTimeout bounds one run of the command, and
// is passed as an argument for the same reason.
const githubActionsIdentityTokenTimeout = 30 * time.Second

// githubActionsMaxResponseSize bounds the response read, as the SDK bounds
// the exchange's. A token is a few kilobytes.
const githubActionsMaxResponseSize = 1 << 20

// githubActionsModel is the github_actions identity token source.
type githubActionsModel struct {
	Audience types.String `tfsdk:"audience"`
}

func githubActionsSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "Fetch the identity token from GitHub Actions' OIDC endpoint, with no shell, `curl` or `jq` on " +
			"the runner: one request per exchange to ACTIONS_ID_TOKEN_REQUEST_URL, authenticated with " +
			"ACTIONS_ID_TOKEN_REQUEST_TOKEN, so the single-use token is never presented twice. GitHub sets both " +
			"variables only in a job with `permissions: id-token: write`; without them the plan fails. " +
			"`github_actions = {}` requests the default audience. The request is stopped after 30 seconds.",
		Attributes: map[string]schema.Attribute{
			"audience": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				Description: "Audience (`aud` claim) to request. Defaults to `https://api.anthropic.com`, the audience " +
					"a federation rule expects when its `match.audience` is unset; set it to the rule's `match.audience` otherwise.",
			},
		},
	}
}

// githubActionsAudience returns the audience the github_actions source
// requests, and false when the source is not configured.
func githubActionsAudience(ctx context.Context, obj types.Object) (string, bool, error) {
	if obj.IsNull() {
		return "", false, nil
	}
	var m githubActionsModel
	if d := obj.As(ctx, &m, basetypes.ObjectAsOptions{}); d.HasError() {
		return "", false, errors.New("github_actions must be an object")
	}
	if audience := m.Audience.ValueString(); audience != "" {
		return audience, true, nil
	}
	return githubActionsDefaultAudience, true, nil
}

// githubActionsIdentityTokenFromEnvironment reads the two variables GitHub
// sets in a job granted `permissions: id-token: write`. They are read once,
// when the provider is configured: they are fixed for the life of the job, so
// a missing one fails the plan with a diagnostic naming the permission,
// rather than the first exchange with a transport error.
func githubActionsIdentityTokenFromEnvironment(audience string) (option.IdentityTokenFunc, error) {
	requestURL, requestToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	var unset string
	switch {
	case requestURL == "" && requestToken == "":
		unset = "neither is set"
	case requestURL == "":
		unset = "ACTIONS_ID_TOKEN_REQUEST_URL is not set"
	case requestToken == "":
		unset = "ACTIONS_ID_TOKEN_REQUEST_TOKEN is not set"
	}
	if unset != "" {
		return nil, fmt.Errorf("github_actions reads ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN, "+
			"which GitHub Actions sets only in a job with `permissions: id-token: write`, and %s. Grant that "+
			"permission to the job that runs Terraform", unset)
	}

	endpoint, err := githubActionsTokenURL(requestURL, audience)
	if err != nil {
		return nil, err
	}
	return githubActionsIdentityToken(endpoint, requestToken, githubActionsIdentityTokenTimeout), nil
}

// githubActionsTokenURL appends the audience to the request URL, escaped as a
// query value. The runner's URL already carries a query (api-version), kept
// as is, hence `&audience=` as in the Actions toolkit.
func githubActionsTokenURL(requestURL, audience string) (string, error) {
	u, err := url.Parse(requestURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("github_actions: ACTIONS_ID_TOKEN_REQUEST_URL is not an http(s) URL")
	}
	query := "audience=" + url.QueryEscape(audience)
	if u.RawQuery != "" {
		query = u.RawQuery + "&" + query
	}
	u.RawQuery = query
	return u.String(), nil
}

// githubActionsIdentityToken requests a fresh token from endpoint on each
// call, stopping after timeout. One request per exchange, never retried: each
// answer is a new token, and the exchange accepts each `jti` only once.
// Neither token reaches an error or the log: the request token authenticates
// the job to GitHub, and a successful response holds the identity token.
func githubActionsIdentityToken(endpoint, requestToken string, timeout time.Duration) option.IdentityTokenFunc {
	return func(ctx context.Context) (string, error) {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", fmt.Errorf("github_actions: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+requestToken)
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			switch {
			case ctx.Err() != nil:
				// Cancelled by the caller, not failed.
				return "", ctx.Err()
			case errors.Is(reqCtx.Err(), context.DeadlineExceeded):
				return "", fmt.Errorf("github_actions: GitHub's OIDC token endpoint did not answer within %s", timeout)
			}
			return "", fmt.Errorf("github_actions: requesting the OIDC token: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		return readGitHubActionsToken(ctx, resp)
	}
}

// readGitHubActionsToken reads `.value` from a 200 answer. The body of any
// other answer is an error message, logged at TF_LOG=DEBUG like the command's
// stderr; a 200 body is never logged or quoted, malformed or not.
func readGitHubActionsToken(ctx context.Context, resp *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, githubActionsMaxResponseSize))
	if err != nil {
		return "", fmt.Errorf("github_actions: reading the OIDC token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		tflog.Debug(ctx, "github_actions token request failed", map[string]any{
			"status": resp.Status,
			"body":   strings.TrimSpace(string(body)),
		})
		return "", fmt.Errorf("github_actions: GitHub's OIDC token endpoint answered %s (its body is logged at TF_LOG=DEBUG)", resp.Status)
	}

	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Value) == "" {
		return "", errors.New("github_actions: GitHub's OIDC token endpoint answered 200 without a token: " +
			"the body is not a JSON object with a non-empty `value`")
	}
	return strings.TrimSpace(payload.Value), nil
}
