// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestCreateRetryWait(t *testing.T) {
	header := func(kv ...string) *http.Response {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return &http.Response{Header: h}
	}
	tests := []struct {
		name  string
		resp  *http.Response
		retry int
		want  time.Duration
	}{
		{"retry-after-ms wins", header("retry-after-ms", "250", "retry-after", "9"), 0, 250 * time.Millisecond},
		{"retry-after in seconds", header("retry-after", "2"), 0, 2 * time.Second},
		{"bounded", header("retry-after", "3600"), 0, MaxCreateRetryAfter},
		{"negative is no wait", header("retry-after-ms", "-5"), 0, 0},
		{"no header: base delay", header(), 0, CreateRetryDelay},
		{"no header: doubles", header(), 1, 2 * CreateRetryDelay},
		{"no response", nil, 1, 2 * CreateRetryDelay},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := createRetryWait(tc.resp, tc.retry); got != tc.want {
				t.Errorf("createRetryWait = %v, want %v", got, tc.want)
			}
		})
	}
}
