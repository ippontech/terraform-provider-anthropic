// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
)

// state is what the ratchet says about one function.
type state int

// The order is the order of the report: failures first.
const (
	added     state = iota // new, above the cap: fails
	crossed                // at the cap or under at the merge base, above it now: fails
	rose                   // above the cap at the merge base, higher now: fails
	touched                // above the cap at the merge base, changed, not higher
	lowered                // above the cap at the merge base, at the cap or under now
	removed                // above the cap at the merge base, gone
	unchanged              // above the cap at the merge base, source unchanged
)

func (s state) fails() bool { return s == added || s == crossed || s == rose }

// entry is a function above the cap in the working tree or at the merge
// base.
type entry struct {
	key       string
	file      string // in the working tree, or at the merge base when removed
	line      int
	score     int // in the working tree, 0 when removed
	baseScore int // at the merge base, 0 when added
	state     state
}

type result struct {
	limit     int
	entries   []entry
	checked   int // functions scored in the working tree
	baseAbove int // functions above the cap at the merge base
}

func (r result) failures() int {
	n := 0
	for _, e := range r.entries {
		if e.state.fails() {
			n++
		}
	}
	return n
}

func (r result) passed() bool { return r.failures() == 0 }

func (r result) count(s state) int {
	n := 0
	for _, e := range r.entries {
		if e.state == s {
			n++
		}
	}
	return n
}

// evaluate compares the working tree (head) with the merge base (base).
// A function above limit at the merge base may change, but its score may not
// rise; any other function, new or not, scores limit or less. A function is
// matched across the trees by its package directory and name, so moving it
// to another file of the package keeps it, while renaming it or moving it to
// another package makes it new (see counterpart for same-named functions).
func evaluate(base, head map[string]function, limit int) result {
	r := result{limit: limit, checked: len(head)}
	for _, h := range head {
		b, inBase := counterpart(h, base)
		if s, ok := classify(h, b, inBase, limit); ok {
			r.entries = append(r.entries, entry{key: h.key, file: h.file, line: h.line, score: h.score, baseScore: b.score, state: s})
		}
	}
	for _, b := range base {
		if b.score <= limit {
			continue
		}
		r.baseAbove++
		if _, ok := counterpart(b, head); !ok {
			r.entries = append(r.entries, entry{key: b.key, file: b.file, line: b.line, baseScore: b.score, state: removed})
		}
	}
	slices.SortFunc(r.entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.state, b.state), cmp.Compare(b.score, a.score), cmp.Compare(b.baseScore, a.baseScore), strings.Compare(a.key, b.key))
	})
	return r
}

// counterpart returns the function of the other tree that fn is: the one
// with the same key, or, when fn is alone of its name in one tree and has a
// same-named sibling in the other, the first one of its file. Adding a second
// init or a build-tagged twin then leaves the existing function matched
// instead of making it read as new, and removing one leaves the other matched.
func counterpart(fn function, other map[string]function) (function, bool) {
	if o, ok := other[fn.key]; ok {
		return o, true
	}
	switch fn.key {
	case fn.name:
		o, ok := other[inFile(fn.name, fn.file)]
		return o, ok
	case inFile(fn.name, fn.file):
		if o, ok := other[fn.name]; ok && o.file == fn.file {
			return o, true
		}
	}
	return function{}, false
}

// classify returns the state of a function of the working tree, and false
// when the report has nothing to say about it (at the cap or under, and so
// it was at the merge base).
func classify(h, b function, inBase bool, limit int) (state, bool) {
	switch {
	case h.score <= limit:
		return lowered, inBase && b.score > limit
	case !inBase:
		return added, true
	case b.score <= limit:
		return crossed, true
	case h.score > b.score:
		return rose, true
	case bytes.Equal(h.src, b.src):
		return unchanged, true
	default:
		return touched, true
	}
}

// report holds what the Markdown report shows besides the result.
type report struct {
	mergeBase string // abbreviated commit
	dir       string
	exclude   string
	gocognit  string // module version, "" when unknown
}

// writeReport prints the rules, the functions the change concerns and, folded,
// the functions above the cap that it leaves alone.
func writeReport(w io.Writer, r result, rep report) error {
	var b strings.Builder
	b.WriteString("## Cognitive complexity ratchet\n\n")
	if r.passed() {
		b.WriteString("All rules pass.\n\n")
	} else {
		b.WriteString("**The ratchet fails**: see the functions marked FAIL.\n\n")
	}
	fmt.Fprintf(&b, "| Rule | Functions | Result |\n|---|---:|---|\n")
	fmt.Fprintf(&b, "| A new function scores %d or less | %d above | %s |\n", r.limit, r.count(added), verdict(r.count(added) == 0))
	fmt.Fprintf(&b, "| A function at %d or less at the merge base stays at %d or less | %d above | %s |\n", r.limit, r.limit, r.count(crossed), verdict(r.count(crossed) == 0))
	fmt.Fprintf(&b, "| A function above %d at the merge base may change, its score may not rise | %d rose, %d changed without rising, %d brought to %d or less | %s |\n\n",
		r.limit, r.count(rose), r.count(touched), r.count(lowered), r.limit, verdict(r.count(rose) == 0))

	if r.baseAbove == 0 {
		fmt.Fprintf(&b, "No function scores above %d at the merge base (`%s`): the cap is absolute.\n\n", r.limit, rep.mergeBase)
	} else {
		fmt.Fprintf(&b, "%d functions score above %d at the merge base (`%s`). Once none is left, the cap is absolute.\n\n", r.baseAbove, r.limit, rep.mergeBase)
	}
	if !r.passed() {
		fmt.Fprintf(&b, "To fix a failure, split the function: move a branch, a loop body or a block of mapping into a helper whose name says what it does. A function above %d at the merge base can be changed without splitting it as long as its score does not rise: put the new logic in a helper rather than in the function.\n\n", r.limit)
	}

	writeTable(&b, "### Functions this change concerns\n\n", r.entries, func(e entry) bool { return e.state != unchanged }, r.limit)
	if n := r.count(unchanged); n > 0 {
		fmt.Fprintf(&b, "<details><summary>%d other functions above %d at the merge base, unchanged</summary>\n\n", n, r.limit)
		writeTable(&b, "", r.entries, func(e entry) bool { return e.state == unchanged }, r.limit)
		b.WriteString("</details>\n\n")
	}

	version := "gocognit"
	if rep.gocognit != "" {
		version += " " + rep.gocognit
	}
	fmt.Fprintf(&b, "Scope: %d functions in the non-test Go files under `%s`, outside `%s` and generated files. Scores from %s, file by file as golangci-lint computes them; `//gocognit:ignore` and `//nolint` exempt nothing here.\n",
		r.checked, rep.dir, rep.exclude, version)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeTable(b *strings.Builder, title string, entries []entry, keep func(entry) bool, limit int) {
	var rows []entry
	for _, e := range entries {
		if keep(e) {
			rows = append(rows, e)
		}
	}
	if len(rows) == 0 {
		return
	}
	b.WriteString(title)
	b.WriteString("| Function | File | Score | Merge base | Result |\n|---|---|---:|---:|---|\n")
	for _, e := range rows {
		fmt.Fprintf(b, "| `%s` | `%s:%d` | %s | %s | %s |\n", e.key, e.file, e.line, scoreText(e.score, e.state == removed), scoreText(e.baseScore, e.state == added), describe(e, limit))
	}
	b.WriteString("\n")
}

func scoreText(score int, absent bool) string {
	if absent {
		return "-"
	}
	return fmt.Sprint(score)
}

func describe(e entry, limit int) string {
	switch e.state {
	case added:
		return fmt.Sprintf("**FAIL**: new function above %d", limit)
	case crossed:
		return fmt.Sprintf("**FAIL**: was %d or less at the merge base", limit)
	case rose:
		return fmt.Sprintf("**FAIL**: rose from %d", e.baseScore)
	case touched:
		if e.score < e.baseScore {
			return fmt.Sprintf("changed, lowered from %d (pass)", e.baseScore)
		}
		return "changed, not higher (pass)"
	case lowered:
		return fmt.Sprintf("brought to %d or less", limit)
	case removed:
		return "gone: deleted, renamed or moved to another package"
	default:
		return "unchanged"
	}
}

func verdict(ok bool) string {
	if ok {
		return "pass"
	}
	return "**FAIL**"
}
