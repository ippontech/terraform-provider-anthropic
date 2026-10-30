// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"context"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// ModelInfoCache memoizes Models API lookups (GET /v1/models/{model_id},
// https://platform.claude.com/docs/en/api/models/retrieve) by the model ID or
// alias asked for. ProviderData.Models returns the one of a provider
// configuration.
//
// It is safe for concurrent use: Terraform plans resources in parallel, and
// concurrent lookups of one model share a single call. A failed lookup is
// cached like a successful one, so an API that cannot be reached costs one
// attempt per model rather than one per resource. The zero value is ready to
// use, and a nil *ModelInfoCache caches nothing.
type ModelInfoCache struct {
	mu      sync.Mutex
	entries map[string]*modelInfoEntry
}

type modelInfoEntry struct {
	once sync.Once
	info *anthropic.ModelInfo
	err  error
}

// Get returns what fetch(ctx, id) returns, calling fetch only for the first
// Get of id; later and concurrent callers get the same result. fetch must be
// the lookup of the configuration's standard client, since the cache is keyed
// by model only. The returned ModelInfo is shared: callers must not modify it.
func (c *ModelInfoCache) Get(ctx context.Context, id string, fetch func(context.Context, string) (*anthropic.ModelInfo, error)) (*anthropic.ModelInfo, error) {
	if c == nil {
		return fetch(ctx, id)
	}
	c.mu.Lock()
	e, ok := c.entries[id]
	if !ok {
		if c.entries == nil {
			c.entries = map[string]*modelInfoEntry{}
		}
		e = &modelInfoEntry{}
		c.entries[id] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.info, e.err = fetch(ctx, id) })
	return e.info, e.err
}
