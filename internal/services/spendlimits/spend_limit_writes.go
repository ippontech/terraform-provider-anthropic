// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits

import (
	"sync"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// spendLimitWrites records, for one provider configuration (its Admin API
// client) and for the life of the provider process, that is one apply, the
// spend limits a set wrote, and holds a lock per (scope, period) pair.
//
// The set endpoint upserts on the pair and keeps the limit's ID, so a create
// can adopt the very limit another resource instance destroys in the same
// apply: `count` going from [A, B] to [B], instances re-keyed without `moved`
// blocks, a create_before_destroy replacement. Deleting that ID would remove
// the cap the other instance has just set and still tracks, so Delete leaves
// a limit set during this apply in place. The pair's lock, held across a set
// and across Delete's check and request, keeps the two from interleaving.
type spendLimitWrites struct {
	locks sync.Map // pair key -> *sync.Mutex

	mu  sync.Mutex
	ids map[string]struct{}
}

// writesByClient maps each *admin.Client (one per provider configuration) to
// its spendLimitWrites.
var writesByClient sync.Map

func writesFor(client *admin.Client) *spendLimitWrites {
	if w, ok := writesByClient.Load(client); ok {
		return w.(*spendLimitWrites)
	}
	w, _ := writesByClient.LoadOrStore(client, &spendLimitWrites{ids: map[string]struct{}{}})
	return w.(*spendLimitWrites)
}

// lock locks the (scope, period) pair and returns the function that unlocks it.
func (w *spendLimitWrites) lock(scope spendLimitScopeAPI, period string) (unlock func()) {
	key := scope.Type + "\x00" + scope.UserID + "\x00" + scope.WorkspaceID + "\x00" + period
	m, _ := w.locks.LoadOrStore(key, &sync.Mutex{})
	m.(*sync.Mutex).Lock()
	return m.(*sync.Mutex).Unlock
}

func (w *spendLimitWrites) recordSet(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ids[id] = struct{}{}
}

func (w *spendLimitWrites) wasSet(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.ids[id]
	return ok
}
