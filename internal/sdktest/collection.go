// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// UpdateMode is how Collection.Update applies a request body to a stored
// object. Fields the body omits are kept in every mode.
type UpdateMode int

const (
	// Replace sets every field the body carries, a map as a whole and a null
	// as null.
	Replace UpdateMode = iota + 1
	// Patch is Replace, except that a map field merges key by key and a key
	// set to null is deleted. Reference: "metadata: Metadata patch. Set a
	// key to a string to upsert it, or to null to delete it" on
	// https://platform.claude.com/docs/en/api/beta/agents/update (vaults,
	// memory stores and deployments use the same wording).
	Patch
	// PatchNullOrEmpty is Patch, except that an empty string deletes the key
	// too. Reference: "Set a value to null or empty string to delete the
	// key" on https://platform.claude.com/docs/en/api/beta/environments/update.
	PatchNullOrEmpty
)

// baseTime is the created_at of the first object of every Collection; each
// write moves the clock one second, so timestamps are deterministic.
var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Collection is an in-memory store of API objects (JSON objects as
// map[string]any) with the HTTP handlers of a CRUD API around it. Objects are
// listed in creation order.
type Collection struct {
	s        *Server
	idPrefix string

	mu    sync.Mutex
	clock int
	// order holds every key ever stored, deleted ones included, and pos the
	// rank of every id ever stored, so a cursor naming an object deleted
	// since it was handed out still has a position.
	order []string
	seen  map[string]bool
	pos   map[string]int
	items map[string]map[string]any
	key   string
}

// NewCollection returns an empty collection whose generated IDs start with
// idPrefix ("env_", "skill_").
func (s *Server) NewCollection(idPrefix string) *Collection {
	return &Collection{
		s:        s,
		idPrefix: idPrefix,
		seen:     map[string]bool{},
		pos:      map[string]int{},
		items:    map[string]map[string]any{},
		key:      "id",
	}
}

// KeyedBy makes field, instead of "id", the key Get, Update, Delete and the
// handlers look objects up by (skill versions are addressed by "version").
// Call it before storing anything.
func (c *Collection) KeyedBy(field string) *Collection {
	c.key = field
	return c
}

func (c *Collection) tick() string {
	c.clock++
	return baseTime.Add(time.Duration(c.clock) * time.Second).Format(time.RFC3339)
}

// Create stores a copy of obj and returns the stored object. It sets id
// (idPrefix and a sequence number) unless obj has one, and created_at and
// updated_at unless obj has them.
func (c *Collection) Create(obj map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := deepCopy(obj)
	now := c.tick()
	if _, ok := o["id"]; !ok {
		o["id"] = fmt.Sprintf("%s%024d", c.idPrefix, len(c.order)+1)
	}
	for _, f := range []string{"created_at", "updated_at"} {
		if _, ok := o[f]; !ok {
			o[f] = now
		}
	}
	c.store(o)
	return deepCopy(o)
}

// Put stores a copy of obj as is, replacing any object with the same key.
// Tests use it to seed objects created outside Terraform.
func (c *Collection) Put(obj map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store(deepCopy(obj))
}

func (c *Collection) store(o map[string]any) {
	k := fmt.Sprint(o[c.key])
	if !c.seen[k] {
		c.seen[k] = true
		c.pos[idOf(o)] = len(c.order)
		c.order = append(c.order, k)
	}
	c.items[k] = o
}

// Get returns a copy of the object stored under key.
func (c *Collection) Get(key string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.items[key]
	if !ok {
		return nil, false
	}
	return deepCopy(o), true
}

// Has reports whether an object is stored under key.
func (c *Collection) Has(key string) bool {
	_, ok := c.Get(key)
	return ok
}

// Len returns the number of stored objects.
func (c *Collection) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// List returns copies of the stored objects for which keep returns true (all
// of them when keep is nil), in creation order.
func (c *Collection) List(keep func(map[string]any) bool) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, k := range c.order {
		if o, ok := c.items[k]; ok && (keep == nil || keep(o)) {
			out = append(out, deepCopy(o))
		}
	}
	return out
}

// Update applies body to the object stored under key in mode, bumps its
// updated_at and returns the result.
func (c *Collection) Update(key string, body map[string]any, mode UpdateMode) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.items[key]
	if !ok {
		return nil, false
	}
	for f, v := range deepCopy(body) {
		patch, isMap := v.(map[string]any)
		stored, storedIsMap := o[f].(map[string]any)
		if mode == Replace || !isMap {
			o[f] = v
			continue
		}
		if !storedIsMap {
			stored = map[string]any{}
		}
		for k, pv := range patch {
			if pv == nil || (mode == PatchNullOrEmpty && pv == "") {
				delete(stored, k)
				continue
			}
			stored[k] = pv
		}
		o[f] = stored
	}
	o["updated_at"] = c.tick()
	return deepCopy(o), true
}

// Delete removes the object stored under key and reports whether it existed.
func (c *Collection) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	delete(c.items, key)
	return ok
}

// Archive sets archived_at on the object stored under key, unless it is
// already set (archiving is idempotent), and returns the object.
// Reference: "archived_at: string or null" on
// https://platform.claude.com/docs/en/api/beta/environments/archive.
func (c *Collection) Archive(key string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if at, _ := o["archived_at"].(string); at == "" {
		now := c.tick()
		o["archived_at"] = now
		o["updated_at"] = now
	}
	return deepCopy(o), true
}

// HandleCreate registers route to create an object from the JSON body and
// answer it with 200. The parent wildcards of route ({skill_id}) are copied
// into the object.
func (c *Collection) HandleCreate(route string) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		body, ok := c.decodeBody(w, r)
		if !ok {
			return
		}
		for _, name := range wildcards(route) {
			body[name] = r.PathValue(name)
		}
		WriteJSON(w, http.StatusOK, c.Create(body))
	})
}

// HandleGet registers route to answer the object its last wildcard names, or
// a 404 not_found_error. The other wildcards must match the object's field
// of the same name, as a child object is only found under its parent.
func (c *Collection) HandleGet(route string) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		if o, ok := c.lookup(w, r, route); ok {
			WriteJSON(w, http.StatusOK, o)
		}
	})
}

// HandleUpdate registers route to apply the JSON body to the object its last
// wildcard names, in mode, and answer the result.
func (c *Collection) HandleUpdate(route string, mode UpdateMode) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := c.lookup(w, r, route); !ok {
			return
		}
		body, ok := c.decodeBody(w, r)
		if !ok {
			return
		}
		o, _ := c.Update(lastWildcardValue(r, route), body, mode)
		WriteJSON(w, http.StatusOK, o)
	})
}

// HandleDelete registers route to delete the object its last wildcard names
// and answer {"id": ..., "type": deletedType}, the shape of every delete
// response in the reference (e.g. "type: skill_deleted" on
// https://platform.claude.com/docs/en/api/beta/skills/delete).
func (c *Collection) HandleDelete(route, deletedType string) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		o, ok := c.lookup(w, r, route)
		if !ok {
			return
		}
		c.Delete(lastWildcardValue(r, route))
		WriteJSON(w, http.StatusOK, map[string]any{"id": o["id"], "type": deletedType})
	})
}

// HandleArchive registers route to archive the object its last wildcard
// names and answer it.
func (c *Collection) HandleArchive(route string) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := c.lookup(w, r, route); !ok {
			return
		}
		o, _ := c.Archive(lastWildcardValue(r, route))
		WriteJSON(w, http.StatusOK, o)
	})
}

// HandleList registers route to list the objects whose fields match the
// route's wildcards, in pages (see Server.Paginate). keep, when not nil,
// filters further, typically on query parameters.
func (c *Collection) HandleList(route string, family Family, pageSize int, keep func(r *http.Request, obj map[string]any) bool) {
	c.s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		items := c.List(func(o map[string]any) bool {
			for _, name := range wildcards(route) {
				if fmt.Sprint(o[name]) != r.PathValue(name) {
					return false
				}
			}
			return keep == nil || keep(r, o)
		})
		c.s.paginate(w, r, family, pageSize, items, idOf, c.position)
	})
}

// position returns the creation rank of the object whose id is id, deleted
// or not.
func (c *Collection) position(id string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pos[id]
	return p, ok
}

// lookup finds the object a request names, or answers 404.
func (c *Collection) lookup(w http.ResponseWriter, r *http.Request, route string) (map[string]any, bool) {
	names := wildcards(route)
	if len(names) == 0 {
		c.s.t.Errorf("sdktest: route %q has no wildcard to look an object up by", route)
		WriteAPIError(w, http.StatusBadRequest, "", "sdktest: route without wildcard")
		return nil, false
	}
	o, ok := c.Get(r.PathValue(names[len(names)-1]))
	if ok {
		for _, name := range names[:len(names)-1] {
			if fmt.Sprint(o[name]) != r.PathValue(name) {
				ok = false
			}
		}
	}
	if !ok {
		WriteAPIError(w, http.StatusNotFound, "", fmt.Sprintf("%s not found", r.URL.Path))
		return nil, false
	}
	return o, true
}

func (c *Collection) decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	return c.s.DecodeBody(w, r)
}

var wildcardRE = regexp.MustCompile(`\{([^}.]+)(\.\.\.)?\}`)

// wildcards returns the wildcard names of a route pattern, in order.
func wildcards(route string) []string {
	var names []string
	for _, m := range wildcardRE.FindAllStringSubmatch(route, -1) {
		names = append(names, m[1])
	}
	return names
}

func lastWildcardValue(r *http.Request, route string) string {
	names := wildcards(route)
	return r.PathValue(names[len(names)-1])
}

// DeepCopy returns a copy of a JSON object that shares nothing with it,
// numbers as json.Number (as DecodeBody and Collection keep them), or nil for
// nil. A fake hands copies out of its store, so a response encoded after its
// lock is released does not race a later write. A value JSON cannot encode
// panics.
func DeepCopy(o map[string]any) map[string]any {
	if o == nil {
		return nil
	}
	return deepCopy(o)
}

// deepCopy copies a JSON object through encoding/json, keeping numbers as
// json.Number.
func deepCopy(o map[string]any) map[string]any {
	b, err := json.Marshal(o)
	if err != nil {
		panic(fmt.Sprintf("sdktest: object is not JSON: %v", err))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	out := map[string]any{}
	if err := dec.Decode(&out); err != nil {
		panic(fmt.Sprintf("sdktest: object is not JSON: %v", err))
	}
	return out
}
