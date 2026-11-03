// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// workspaceHeader is the header the standard client sends on every request
// when the provider's workspace_id argument is set (internal/provider).
const workspaceHeader = "anthropic-workspace-id"

// IsNotFound reports whether err is, or wraps, the API's answer that the
// object a request names does not exist: an SDK API error (*anthropic.Error,
// from the standard and OAuth clients) with status 404 and the not_found_error
// envelope, that is not a workspace 404. Errors from internal/admin use
// admin.IsNotFound instead.
//
// A workspace 404 answers a request that named a workspace in the
// anthropic-workspace-id header and could not use it: a single-workspace key
// answers 404 "Workspace not found" to any such header, its own workspace
// included (CLAUDE.md, "Credentials and clients"). The object may well
// exist, so it is not a not-found; neither is a 404 without the API's
// not_found_error envelope (a proxy, a wrong base URL). The response cannot
// tell a deleted object from one that lives outside the workspace the key
// reaches without a header, so that case still reads as not found.
//
// Resources use it for the 404 convention: Read removes the resource from
// state (the object was deleted out of band, so the next plan re-creates it),
// and Delete treats the 404 as success (the object is already gone). Any
// other error, a workspace 404 included, stays a diagnostic.
func IsNotFound(err error) bool {
	var apiErr *anthropic.Error
	if !stderrors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound || apiErr.Type() != anthropic.ErrorTypeNotFoundError {
		return false
	}
	return !isWorkspaceNotFound(apiErr)
}

// isWorkspaceNotFound reports whether a 404 answers the workspace a request
// named rather than the object: the request carried anthropic-workspace-id
// and the error message is about a workspace.
func isWorkspaceNotFound(apiErr *anthropic.Error) bool {
	if apiErr.Request == nil || apiErr.Request.Header.Get(workspaceHeader) == "" {
		return false
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(apiErr.RawJSON()), &envelope)
	return strings.Contains(strings.ToLower(envelope.Error.Message), "workspace")
}
