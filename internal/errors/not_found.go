// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	stderrors "errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
)

// IsNotFound reports whether err is, or wraps, an SDK API error with status
// 404. The Go SDK has a single error type for every status (*anthropic.Error),
// so callers branch on the status code; this is that check, for errors from
// the standard and OAuth clients. Errors from internal/admin use
// admin.IsNotFound instead.
//
// Resources use it for the 404 convention: Read removes the resource from
// state (the object was deleted out of band, so the next plan re-creates it),
// and Delete treats the 404 as success (the object is already gone).
func IsNotFound(err error) bool {
	var apiErr *anthropic.Error
	return stderrors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
