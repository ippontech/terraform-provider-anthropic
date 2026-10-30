// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// JSONDiff compares two JSON documents as values and returns "" when they are
// equal, or one line per differing path otherwise:
//
//	$.metadata.team: got "a", want absent
//	$.tools[1].name: got "bash", want "read"
//
// Key order and spacing do not matter; an absent key, a null and an empty
// string are three different things, which is what PATCH bodies hinge on.
// Numbers compare by their decimal text, so 1 and 1.0 differ.
func JSONDiff(got, want string) string {
	g, err := decodeJSON(got)
	if err != nil {
		return fmt.Sprintf("got is not JSON (%v): %s", err, got)
	}
	w, err := decodeJSON(want)
	if err != nil {
		return fmt.Sprintf("want is not JSON (%v): %s", err, want)
	}
	var lines []string
	diffValues("$", g, w, &lines)
	return strings.Join(lines, "\n")
}

func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the first value")
	}
	return v, nil
}

func diffValues(path string, got, want any, lines *[]string) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*lines = append(*lines, fmt.Sprintf("%s: got %s, want an object", path, render(got)))
			return
		}
		keys := make([]string, 0, len(g)+len(w))
		for k := range g {
			keys = append(keys, k)
		}
		for k := range w {
			if _, ok := g[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			gv, gok := g[k]
			wv, wok := w[k]
			p := path + "." + k
			switch {
			case !gok:
				*lines = append(*lines, fmt.Sprintf("%s: got absent, want %s", p, render(wv)))
			case !wok:
				*lines = append(*lines, fmt.Sprintf("%s: got %s, want absent", p, render(gv)))
			default:
				diffValues(p, gv, wv, lines)
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			*lines = append(*lines, fmt.Sprintf("%s: got %s, want an array", path, render(got)))
			return
		}
		if len(g) != len(w) {
			*lines = append(*lines, fmt.Sprintf("%s: got %d elements %s, want %d %s", path, len(g), render(g), len(w), render(w)))
			return
		}
		for i := range w {
			diffValues(fmt.Sprintf("%s[%d]", path, i), g[i], w[i], lines)
		}
	default:
		if render(got) != render(want) {
			*lines = append(*lines, fmt.Sprintf("%s: got %s, want %s", path, render(got), render(want)))
		}
	}
}

// render prints a decoded JSON value compactly, with sorted keys.
func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
