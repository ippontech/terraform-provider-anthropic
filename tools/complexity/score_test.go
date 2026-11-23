// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"slices"
	"testing"
)

// A method is named after its receiver type, type parameters and parentheses
// left out, so that it keeps its key when only those change.
func TestScoreFileNamesMethods(t *testing.T) {
	const src = `package a

func Plain() {}

func (v Value) ByValue() {}

func (p *Pointer) ByPointer() {}

func (Unnamed) NoName() {}

func (s *Set[T]) OneParam() {}

func (m Map[K, V]) TwoParams() {}

func (p *Pair[K, V]) TwoParamsByPointer() {}

func (p (*Paren)) Parenthesised() {}
`
	fns, err := scoreFile("internal/a/a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, fn := range fns {
		got = append(got, fn.key)
	}
	want := []string{
		"internal/a.Plain",
		"internal/a.(Value).ByValue",
		"internal/a.(*Pointer).ByPointer",
		"internal/a.(Unnamed).NoName",
		"internal/a.(*Set).OneParam",
		"internal/a.(Map).TwoParams",
		"internal/a.(*Pair).TwoParamsByPointer",
		"internal/a.(*Paren).Parenthesised",
	}
	if !slices.Equal(got, want) {
		t.Errorf("keys:\n got %q\nwant %q", got, want)
	}
}

// Several init functions of one file are told apart by their rank in it.
func TestScoreTreeSameNameInOneFile(t *testing.T) {
	const src = `package a

func init() {}

func init() {
	if x {
	}
}
`
	fns, err := scoreTree(tree{"internal/a/a.go": []byte(src), "internal/a/b.go": []byte("package a\n\nfunc init() {}\n")})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"internal/a.init in internal/a/a.go":   0,
		"internal/a.init in internal/a/a.go#2": 1,
		"internal/a.init in internal/a/b.go":   0,
	}
	if len(fns) != len(want) {
		t.Errorf("scored %d functions, want %d: %v", len(fns), len(want), keys(fns))
	}
	for key, score := range want {
		if fn, ok := fns[key]; !ok || fn.score != score {
			t.Errorf("%s: scored %d (present: %v), want %d", key, fn.score, ok, score)
		}
	}
}
