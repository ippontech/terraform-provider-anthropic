// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// maxBodyExcerpt bounds how much of an unexpected 404 body an error repeats.
const maxBodyExcerpt = 512

// UnservedRouteError is what a request to a gateway platform returns when the
// gateway answers 404 with anything but the Anthropic API's own not_found_error.
//
// Resources read a 404 as "deleted outside Terraform" and drop the object from
// state, so that the next apply recreates it. That reading holds for the API,
// which answers a missing object with a not_found_error body. A gateway that
// does not route a path can also answer 404, with a body of its own, and
// reading that as a deletion would drop a live object from state and create a
// duplicate on the next apply. This error is not an *anthropic.Error, so no
// status check matches it and the resource reports it instead.
type UnservedRouteError struct {
	Platform Platform
	Method   string
	URL      string
	// Body is the start of the response body, at most maxBodyExcerpt bytes.
	Body string
}

func (e *UnservedRouteError) Error() string {
	return fmt.Sprintf("%s %s: %s answered 404 without the Claude API's not_found_error body, so the provider cannot "+
		"tell an object deleted outside Terraform from a route the gateway does not serve, and keeps the object in "+
		"state. Check that %s serves this resource (%s). Response body: %q",
		e.Method, e.URL, e.Platform.DisplayName(), e.Platform.DisplayName(), e.Platform.DocURL(), e.Body)
}

// NotFoundGuard returns the middleware installed on every SDK client of a
// gateway platform, never on a first-party one. A 404 whose body is the Claude
// API's not_found_error passes through untouched; any other 404 becomes an
// *UnservedRouteError.
//
// The response is returned with the error, marked x-should-retry: false: the
// SDK retries a request that ends without a response, and a gateway that does
// not route a path will not start routing it on the next attempt.
func NotFoundGuard(p Platform) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		res, err := next(req)
		if err != nil || res == nil || res.StatusCode != http.StatusNotFound {
			return res, err
		}

		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read the 404 response body from %s: %w", p.DisplayName(), readErr)
		}
		res.Body = io.NopCloser(bytes.NewReader(body))

		if IsAPINotFound(body) {
			return res, nil
		}

		res.Header.Set("x-should-retry", "false")
		excerpt := body
		if len(excerpt) > maxBodyExcerpt {
			excerpt = excerpt[:maxBodyExcerpt]
		}
		return res, &UnservedRouteError{
			Platform: p,
			Method:   req.Method,
			URL:      req.URL.String(),
			Body:     string(excerpt),
		}
	}
}

// IsAPINotFound reports whether body is the Claude API's error envelope for a
// missing object: {"type": "error", "error": {"type": "not_found_error", ...}}.
func IsAPINotFound(body []byte) bool {
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Type == "error" && envelope.Error.Type == "not_found_error"
}
