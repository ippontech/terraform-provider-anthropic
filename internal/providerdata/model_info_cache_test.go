// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// countingFetch answers every model with its ID, or err, and counts its
// calls per model.
type countingFetch struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
}

func (f *countingFetch) fetch(_ context.Context, id string) (*anthropic.ModelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[id]++
	if f.err != nil {
		return nil, f.err
	}
	return &anthropic.ModelInfo{ID: id}, nil
}

func (f *countingFetch) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

// The zero value caches each model, its error included; a nil cache caches
// nothing.
func TestModelInfoCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var c ModelInfoCache
	f := &countingFetch{}
	for range 3 {
		for _, id := range []string{"m1", "m2"} {
			info, err := c.Get(ctx, id, f.fetch)
			if err != nil || info.ID != id {
				t.Fatalf("Get(%s) = %v, %v, want the model", id, info, err)
			}
		}
	}
	if f.count("m1") != 1 || f.count("m2") != 1 {
		t.Errorf("fetch calls = %v, want one per model", f.calls)
	}

	failing := &countingFetch{err: errors.New("unreachable")}
	for range 2 {
		if _, err := c.Get(ctx, "m3", failing.fetch); err == nil || err.Error() != "unreachable" {
			t.Fatalf("Get(m3) error = %v, want the fetch error", err)
		}
	}
	if failing.count("m3") != 1 {
		t.Errorf("failing fetch called %d times, want 1: errors are cached", failing.count("m3"))
	}

	var none *ModelInfoCache
	g := &countingFetch{}
	for range 2 {
		if info, err := none.Get(ctx, "m1", g.fetch); err != nil || info.ID != "m1" {
			t.Fatalf("nil cache Get = %v, %v", info, err)
		}
	}
	if g.count("m1") != 2 {
		t.Errorf("nil cache: fetch called %d times, want 2", g.count("m1"))
	}
}

// Concurrent lookups of one model share a single fetch, which they all wait
// for.
func TestModelInfoCache_concurrent(t *testing.T) {
	t.Parallel()
	var c ModelInfoCache
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(ctx context.Context, id string) (*anthropic.ModelInfo, error) {
		calls.Add(1)
		<-release
		return &anthropic.ModelInfo{ID: id}, ctx.Err()
	}

	const n = 16
	var wg, started sync.WaitGroup
	started.Add(n)
	results := make([]*anthropic.ModelInfo, n)
	for i := range n {
		wg.Go(func() {
			started.Done()
			results[i], _ = c.Get(context.Background(), "m", fetch)
		})
	}
	started.Wait()
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("fetch called %d times, want 1", calls.Load())
	}
	for i, r := range results {
		if r == nil || r != results[0] {
			t.Errorf("result %d = %p, want the shared %p", i, r, results[0])
		}
	}
}

// Each ProviderData has its own cache, the same on every call.
func TestProviderDataModels(t *testing.T) {
	t.Parallel()
	a, b := &ProviderData{}, &ProviderData{}
	if first, again := a.Models(), a.Models(); first != again {
		t.Error("Models() changes between calls")
	}
	if a.Models() == b.Models() {
		t.Error("two ProviderData share one cache")
	}
}
