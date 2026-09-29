// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newStaleThenFreshMemoryServer serves the memory as it was before the write
// (stale content_sha256/updated_at) for the first staleReads Get calls, then
// as it is after. Mirrors newStaleThenFreshVaultServer in the vaults package.
func newStaleThenFreshMemoryServer(t *testing.T, memoryID string, before, after time.Time, beforeSha, afterSha string, staleReads int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gets++
		updated := after
		sha := afterSha
		if gets <= staleReads {
			updated = before
			sha = beforeSha
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":                 memoryID,
			"memory_store_id":    "memstore_01ABC",
			"path":               "/notes.md",
			"content":            "content",
			"content_sha256":     sha,
			"content_size_bytes": 7,
			"memory_version_id":  "memver_01ABC",
			"type":               "memory",
			"created_at":         before.Format(time.RFC3339Nano),
			"updated_at":         updated.Format(time.RFC3339Nano),
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

func newTestMemoryClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	return &c
}

// TestAwaitMemoryUpdateVisible_pollsUntilFresh mirrors the vaults regression
// test: a Get answering with the pre-update content_sha256/updated_at must be
// retried until the response reflects the write.
func TestAwaitMemoryUpdateVisible_pollsUntilFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshMemoryServer(t, "mem_01ABC", before, after, "shabefore", "shaafter", 3)
	client := newTestMemoryClient(t, srv)

	awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", after, "shaafter", 2*time.Second, time.Millisecond)

	if *gets != 4 {
		t.Errorf("Get calls = %d, want 4 (three stale reads then the fresh one)", *gets)
	}
}

// A Get that already reflects the write must not be polled a second time.
func TestAwaitMemoryUpdateVisible_returnsImmediatelyWhenFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshMemoryServer(t, "mem_01ABC", before, after, "shabefore", "shaafter", 0)
	client := newTestMemoryClient(t, srv)

	awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", after, "shaafter", 2*time.Second, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

// An updated_at newer than the write but a content_sha256 that still doesn't
// match must keep polling: a rapid second write could otherwise be mistaken
// for the visibility of the first.
func TestAwaitMemoryUpdateVisible_ShaMismatchKeepsPolling(t *testing.T) {
	writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	// The server always answers fresh on updated_at but with the wrong sha,
	// so the wait must run to the timeout.
	srv, gets := newStaleThenFreshMemoryServer(t, "mem_01ABC", writtenAt, writtenAt, "wrongsha", "wrongsha", 0)
	client := newTestMemoryClient(t, srv)

	start := time.Now()
	awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", writtenAt, "expectedsha", 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

// The wait is best-effort: a memory that never converges must return at the
// timeout rather than hang or surface an error.
func TestAwaitMemoryUpdateVisible_givesUpAtTimeout(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshMemoryServer(t, "mem_01ABC", before, after, "shabefore", "shaafter", 1_000_000)
	client := newTestMemoryClient(t, srv)

	start := time.Now()
	awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", after, "shaafter", 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

// A cancelled context must abort the wait promptly.
func TestAwaitMemoryUpdateVisible_honoursContextCancellation(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, _ := newStaleThenFreshMemoryServer(t, "mem_01ABC", before, after, "shabefore", "shaafter", 1_000_000)
	client := newTestMemoryClient(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	awaitMemoryUpdateVisible(ctx, client, "memstore_01ABC", "mem_01ABC", after, "shaafter", 10*time.Second, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return on a cancelled context", elapsed)
	}
}

// newFailingMemoryServer answers every Get with status, so the poll can never
// converge.
func newFailingMemoryServer(t *testing.T, status int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"not found"}}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

// A memory deleted out-of-band (or a key that lost access to its store)
// answers every Get with a terminal status. The wait must bail out on the
// first such answer instead of polling to the deadline.
func TestAwaitMemoryUpdateVisible_stopsOnTerminalReadError(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, gets := newFailingMemoryServer(t, status)
			client := newTestMemoryClient(t, srv)

			writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

			start := time.Now()
			awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", writtenAt, "sha", 10*time.Second, 50*time.Millisecond)

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want an immediate return on a terminal read error", elapsed)
			}
			if *gets != 1 {
				t.Errorf("Get calls = %d, want 1 (no retry on a terminal status)", *gets)
			}
		})
	}
}

// A non-terminal failure says nothing about whether the memory will become
// visible, so it must keep polling until the deadline like a stale read does.
// 422 is used because the SDK client does not retry it internally.
func TestAwaitMemoryUpdateVisible_keepsPollingOnOtherReadErrors(t *testing.T) {
	srv, gets := newFailingMemoryServer(t, 422)
	client := newTestMemoryClient(t, srv)

	writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	start := time.Now()
	awaitMemoryUpdateVisible(context.Background(), client, "memstore_01ABC", "mem_01ABC", writtenAt, "sha", 120*time.Millisecond, 10*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}
