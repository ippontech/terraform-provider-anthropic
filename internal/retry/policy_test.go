// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// fastPolicy keeps the backoff in the millisecond range.
var fastPolicy = Policy{MaxRetries: DefaultMaxRetries, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

// scriptedServer answers the n-th request with statuses[n] (the last status
// repeats; 200 answers {}), and records each request.
type scriptedServer struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	headers  http.Header // added to every error response
	requests []recordedRequest
}

type recordedRequest struct {
	method     string
	path       string
	body       []byte
	retryCount string
}

func newScriptedServer(t *testing.T, statuses ...int) *scriptedServer {
	t.Helper()
	s := &scriptedServer{statuses: statuses, headers: http.Header{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, recordedRequest{
			method: r.Method, path: r.URL.Path, body: body,
			retryCount: r.Header.Get("X-Stainless-Retry-Count"),
		})
		status := s.statuses[min(n, len(s.statuses)-1)]
		headers := s.headers.Clone()
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		maps.Copy(w.Header(), headers)
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"error","message":"status %d"}}`, status)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *scriptedServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func newPolicyClient(t *testing.T, url string, p Policy, extra ...option.RequestOption) *anthropic.Client {
	t.Helper()
	opts := append([]option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(url),
		option.WithAPIKey("test"),
	}, SDKOptions(p)...)
	c := anthropic.NewClient(append(opts, extra...)...)
	return &c
}

func TestMiddleware_retriesByMethod(t *testing.T) {
	type call func(ctx context.Context, c *anthropic.Client) error
	post := func(path string) call {
		return func(ctx context.Context, c *anthropic.Client) error {
			return c.Post(ctx, path, map[string]any{"name": "x"}, nil)
		}
	}
	get := func(ctx context.Context, c *anthropic.Client) error {
		return c.Get(ctx, "v1/environments/env_1", nil, nil)
	}
	del := func(ctx context.Context, c *anthropic.Client) error {
		return c.Delete(ctx, "v1/environments/env_1", nil, nil)
	}

	tests := []struct {
		name      string
		call      call
		statuses  []int
		header    http.Header
		wantCalls int
		wantErr   bool
	}{
		// Non-idempotent: replayed on 429 and 529 only.
		{name: "POST 500 is not retried", call: post("v1/environments"), statuses: []int{500}, wantCalls: 1, wantErr: true},
		{name: "POST 502 is not retried", call: post("v1/messages"), statuses: []int{502}, wantCalls: 1, wantErr: true},
		{name: "POST 504 is not retried", call: post("v1/messages"), statuses: []int{504}, wantCalls: 1, wantErr: true},
		{name: "POST 408 is not retried", call: post("v1/messages"), statuses: []int{408}, wantCalls: 1, wantErr: true},
		{name: "POST 409 is not retried", call: post("v1/agents/agent_1"), statuses: []int{409}, wantCalls: 1, wantErr: true},
		{name: "POST 429 then 200 is retried", call: post("v1/environments"), statuses: []int{429, 200}, wantCalls: 2},
		{name: "POST 429 gives up after MaxRetries", call: post("v1/environments"), statuses: []int{429}, wantCalls: 3, wantErr: true},
		{name: "POST 529 then 200 is retried", call: post("v1/agents"), statuses: []int{529, 200}, wantCalls: 2},
		{name: "POST 529 gives up after MaxRetries", call: post("v1/messages"), statuses: []int{529}, wantCalls: 3, wantErr: true},
		{name: "POST 400 is not retried", call: post("v1/environments"), statuses: []int{400}, wantCalls: 1, wantErr: true},
		{name: "agent update POST 500 is not retried", call: post("v1/agents/agent_1"), statuses: []int{500}, wantCalls: 1, wantErr: true},
		{name: "memory update POST 503 is not retried", call: post("v1/memory_stores/ms_1/memories/mem_1"), statuses: []int{503}, wantCalls: 1, wantErr: true},
		{name: "deployment run POST 500 is not retried", call: post("v1/deployments/dep_1/run"), statuses: []int{500}, wantCalls: 1, wantErr: true},
		// POSTs that leave the same state when replayed: the SDK's rule.
		{name: "environment update POST 503 then 200 is retried", call: post("v1/environments/env_1"), statuses: []int{503, 200}, wantCalls: 2},
		{name: "vault credential update POST 502 then 200 is retried", call: post("v1/vaults/vlt_1/credentials/cred_1"), statuses: []int{502, 200}, wantCalls: 2},
		{name: "agent archive POST 500 then 200 is retried", call: post("v1/agents/agent_1/archive"), statuses: []int{500, 200}, wantCalls: 2},
		{name: "deployment pause POST 500 then 200 is retried", call: post("v1/deployments/dep_1/pause"), statuses: []int{500, 200}, wantCalls: 2},
		// Idempotent: the SDK's rule.
		{name: "GET 500 then 200 is retried", call: get, statuses: []int{500, 200}, wantCalls: 2},
		{name: "GET 503 gives up after MaxRetries", call: get, statuses: []int{503}, wantCalls: 3, wantErr: true},
		{name: "GET 408 is retried", call: get, statuses: []int{408, 200}, wantCalls: 2},
		{name: "GET 409 is retried", call: get, statuses: []int{409, 200}, wantCalls: 2},
		{name: "GET 429 is retried", call: get, statuses: []int{429, 200}, wantCalls: 2},
		{name: "GET 404 is not retried", call: get, statuses: []int{404}, wantCalls: 1, wantErr: true},
		{name: "DELETE 502 then 200 is retried", call: del, statuses: []int{502, 200}, wantCalls: 2},
		{name: "count_tokens POST 500 is retried", call: post("v1/messages/count_tokens"), statuses: []int{500, 200}, wantCalls: 2},
		// The server's x-should-retry wins either way for an idempotent
		// request, and can only turn a retry off for any other.
		{name: "GET 400 with x-should-retry true", call: get, statuses: []int{400, 200}, header: http.Header{"X-Should-Retry": {"true"}}, wantCalls: 2},
		{name: "GET 500 with x-should-retry false", call: get, statuses: []int{500}, header: http.Header{"X-Should-Retry": {"false"}}, wantCalls: 1, wantErr: true},
		{name: "POST 500 with x-should-retry true is not retried", call: post("v1/environments"), statuses: []int{500, 200}, header: http.Header{"X-Should-Retry": {"true"}}, wantCalls: 1, wantErr: true},
		{name: "POST 429 with x-should-retry false is not retried", call: post("v1/environments"), statuses: []int{429, 200}, header: http.Header{"X-Should-Retry": {"false"}}, wantCalls: 1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newScriptedServer(t, tc.statuses...)
			if tc.header != nil {
				srv.headers = tc.header
			}
			err := tc.call(context.Background(), newPolicyClient(t, srv.URL, fastPolicy))
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := len(srv.recorded()); got != tc.wantCalls {
				t.Errorf("requests = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// Without SDKOptions the SDK retries a POST 500 itself: the behaviour this
// policy replaces, kept here as a control so the table above cannot pass
// for the wrong reason.
func TestSDKDefault_retriesPostOn5xx(t *testing.T) {
	srv := newScriptedServer(t, 500)
	srv.headers = http.Header{"Retry-After-Ms": {"1"}}
	c := anthropic.NewClient(option.WithoutEnvironmentDefaults(), option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	_ = c.Post(context.Background(), "v1/environments", map[string]any{"name": "x"}, nil)
	if got := len(srv.recorded()); got != 3 {
		t.Errorf("requests = %d, want the SDK's 3", got)
	}
}

// A connection lost after the request got one may follow a write that
// landed, so a POST is not replayed; a GET is.
func TestMiddleware_connectionError(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.Method]++
		mu.Unlock()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	c := newPolicyClient(t, srv.URL, fastPolicy)

	if err := c.Post(context.Background(), "v1/environments", map[string]any{"name": "x"}, nil); err == nil {
		t.Error("POST: want an error")
	}
	if err := c.Get(context.Background(), "v1/environments/env_1", nil, nil); err == nil {
		t.Error("GET: want an error")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls[http.MethodPost] != 1 {
		t.Errorf("POST attempts = %d, want 1", calls[http.MethodPost])
	}
	if calls[http.MethodGet] != 3 {
		t.Errorf("GET attempts = %d, want 3", calls[http.MethodGet])
	}
}

// A connection error raised before the request got a connection (here a
// refused dial and a failed TLS handshake) means no byte of it reached the
// API, so even a POST is replayed, as the SDK did.
func TestMiddleware_connectionErrorBeforeConnection(t *testing.T) {
	refused := func(t *testing.T) string {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		_ = l.Close()
		return "http://" + addr
	}
	untrustedTLS := func(t *testing.T) string {
		t.Helper()
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("the handshake should have failed")
		}))
		srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv.URL
	}

	for name, baseURL := range map[string]func(*testing.T) string{"dial refused": refused, "TLS handshake fails": untrustedTLS} {
		t.Run(name, func(t *testing.T) {
			var dials atomic.Int32
			dialer := &net.Dialer{}
			httpClient := &http.Client{Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					dials.Add(1)
					return dialer.DialContext(ctx, network, addr)
				},
			}}
			c := newPolicyClient(t, baseURL(t), fastPolicy, option.WithHTTPClient(httpClient))

			if err := c.Post(context.Background(), "v1/environments", map[string]any{"name": "x"}, nil); err == nil {
				t.Fatal("want an error")
			}
			if got := dials.Load(); got != 3 {
				t.Errorf("POST attempts = %d, want 3", got)
			}
		})
	}
}

// The POSTs that leave the same state when replayed get the idempotent rule;
// creates, uploads, messages, precondition updates and other actions do not.
// The paths are those of the SDK methods the provider calls.
func TestIsIdempotent(t *testing.T) {
	tests := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/v1/agents/agent_1", true},
		{http.MethodDelete, "/v1/environments/env_1", true},
		{http.MethodPost, "/v1/messages/count_tokens", true},
		{http.MethodPost, "/gateway/v1/environments/env_1", true},
		// Updates without a precondition.
		{http.MethodPost, "/v1/environments/env_1", true},
		{http.MethodPost, "/v1/vaults/vlt_1", true},
		{http.MethodPost, "/v1/vaults/vlt_1/credentials/cred_1", true},
		{http.MethodPost, "/v1/memory_stores/ms_1", true},
		{http.MethodPost, "/v1/deployments/dep_1", true},
		{http.MethodPost, "/v1/user_profiles/up_1", true},
		{http.MethodPost, "/v1/organizations/service_accounts/sa_1", true},
		{http.MethodPost, "/v1/organizations/federation_issuers/fdis_1", true},
		{http.MethodPost, "/v1/organizations/federation_rules/fdrl_1", true},
		// State-setting actions.
		{http.MethodPost, "/v1/agents/agent_1/archive", true},
		{http.MethodPost, "/v1/vaults/vlt_1/credentials/cred_1/archive", true},
		{http.MethodPost, "/v1/organizations/federation_rules/fdrl_1/archive", true},
		{http.MethodPost, "/v1/deployments/dep_1/pause", true},
		{http.MethodPost, "/v1/deployments/dep_1/unpause", true},
		// Creates, uploads and messages.
		{http.MethodPost, "/v1/agents", false},
		{http.MethodPost, "/v1/environments", false},
		{http.MethodPost, "/v1/vaults/vlt_1/credentials", false},
		{http.MethodPost, "/v1/memory_stores/ms_1/memories", false},
		{http.MethodPost, "/v1/skills", false},
		{http.MethodPost, "/v1/skills/skill_1/versions", false},
		{http.MethodPost, "/v1/files", false},
		{http.MethodPost, "/v1/messages", false},
		{http.MethodPost, "/v1/messages/batches", false},
		{http.MethodPost, "/v1/organizations/service_accounts", false},
		{http.MethodPost, "/v1/organizations/federation_rules/fdrl_1/workspaces", false},
		// Updates with a precondition, and actions that are not idempotent.
		{http.MethodPost, "/v1/agents/agent_1", false},
		{http.MethodPost, "/v1/memory_stores/ms_1/memories/mem_1", false},
		{http.MethodPost, "/v1/deployments/dep_1/run", false},
		{http.MethodPost, "/v1/tunnels/tnl_1/rotate_token", false},
		{http.MethodPatch, "/v1/environments/env_1", false},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "https://api.anthropic.com"+tc.path+"?beta=true", nil)
			if got := isIdempotent(req); got != tc.want {
				t.Errorf("isIdempotent = %v, want %v", got, tc.want)
			}
		})
	}
}

// A replayed request carries the same body, JSON or multipart (the SDK
// buffers both), and the attempt number in X-Stainless-Retry-Count.
func TestMiddleware_replaysBody(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		srv := newScriptedServer(t, 429, 200)
		c := newPolicyClient(t, srv.URL, fastPolicy)
		if err := c.Post(context.Background(), "v1/environments", map[string]any{"name": "x"}, nil); err != nil {
			t.Fatal(err)
		}
		assertReplayed(t, srv.recorded())
	})

	t.Run("multipart", func(t *testing.T) {
		srv := newScriptedServer(t, 429, 200)
		c := newPolicyClient(t, srv.URL, fastPolicy)
		_, err := c.Beta.Skills.New(context.Background(), anthropic.BetaSkillNewParams{
			Files: []io.Reader{NewNamedReader(strings.NewReader("---\nname: s\n---\n"), "s/SKILL.md")},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := srv.recorded()
		assertReplayed(t, got)
		if !bytes.Contains(got[1].body, []byte("s/SKILL.md")) {
			t.Errorf("replayed multipart body lacks the file part: %q", got[1].body)
		}
	})
}

func assertReplayed(t *testing.T, got []recordedRequest) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2", len(got))
	}
	if len(got[0].body) == 0 || !bytes.Equal(got[0].body, got[1].body) {
		t.Errorf("bodies differ:\n%q\n%q", got[0].body, got[1].body)
	}
	if got[0].retryCount != "0" || got[1].retryCount != "1" {
		t.Errorf("X-Stainless-Retry-Count = %q, %q; want 0, 1", got[0].retryCount, got[1].retryCount)
	}
}

func TestMiddleware_contextCancelledDuringWait(t *testing.T) {
	srv := newScriptedServer(t, 429)
	srv.headers = http.Header{"Retry-After": {"30"}}
	c := newPolicyClient(t, srv.URL, fastPolicy)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Post(ctx, "v1/environments", map[string]any{"name": "x"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v, want the wait cut short by the context", elapsed)
	}
}

func TestPolicyDelay(t *testing.T) {
	header := func(kv ...string) *http.Response {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return &http.Response{Header: h}
	}

	tests := []struct {
		name string
		resp *http.Response
		want time.Duration
	}{
		{name: "retry-after-ms", resp: header("retry-after-ms", "250"), want: 250 * time.Millisecond},
		{name: "retry-after-ms wins over retry-after", resp: header("retry-after-ms", "500", "retry-after", "30"), want: 500 * time.Millisecond},
		{name: "retry-after seconds", resp: header("retry-after", "5"), want: 5 * time.Second},
		{name: "retry-after is capped", resp: header("retry-after", "3600"), want: MaxRetryAfter},
		{name: "retry-after in the past", resp: header("retry-after", "-3"), want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultPolicy.delay(tc.resp, 0); got != tc.want {
				t.Errorf("delay = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("http-date", func(t *testing.T) {
		resp := header("retry-after", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
		if got := DefaultPolicy.delay(resp, 0); got <= 8*time.Second || got > 10*time.Second {
			t.Errorf("delay = %v, want about 10s", got)
		}
	})

	t.Run("exponential backoff without header", func(t *testing.T) {
		for attempt, want := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second} {
			got := DefaultPolicy.delay(&http.Response{Header: http.Header{}}, attempt)
			if got > want || got < want*3/4 {
				t.Errorf("attempt %d: delay = %v, want within [%v, %v]", attempt, got, want*3/4, want)
			}
		}
		if got := DefaultPolicy.delay(nil, 10); got > DefaultMaxDelay {
			t.Errorf("attempt 10: delay = %v, want at most %v", got, DefaultMaxDelay)
		}
	})
}
