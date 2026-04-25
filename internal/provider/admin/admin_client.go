// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	adminAPIBaseURL  = "https://api.anthropic.com"
	AnthropicVersion = "2023-06-01"
)

// Client makes authenticated requests to the Anthropic Admin API (/v1/organizations/*).
// The Anthropic Go SDK does not expose admin endpoints, so this client handles them directly.
type Client struct {
	ApiKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// APIError is returned when the API responds with a non-2xx status code.
type APIError struct {
	StatusCode int
	ErrType    string
	Message    string
}

func (e *APIError) Error() string {
	if e.ErrType != "" {
		return fmt.Sprintf("API error (%d %s): %s", e.StatusCode, e.ErrType, e.Message)
	}
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
}

// IsNotFound returns true when err is an APIError with status 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

func NewClient(apiKey string) *Client {
	return &Client{
		ApiKey:     apiKey,
		BaseURL:    adminAPIBaseURL,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// DoRequest executes an HTTP request against the Admin API and returns the raw response body.
func (c *Client) DoRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}

	req.Header.Set("x-api-key", c.ApiKey)
	req.Header.Set("anthropic-version", AnthropicVersion)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Error.Message != "" {
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				ErrType:    envelope.Error.Type,
				Message:    envelope.Error.Message,
			}
		}
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	return respBody, nil
}
