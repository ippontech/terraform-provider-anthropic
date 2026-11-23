// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCommitTree(t *testing.T) {
	dir := gitRepo(t)

	tr, err := commitTree(dir, "main", fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "base", "internal", "a", "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(tr["internal/a/a.go"]); got != string(want) {
		t.Errorf("internal/a/a.go at main:\n%s\nwant:\n%s", got, want)
	}

	// The head commit holds files the scope leaves out.
	tr, err = commitTree(dir, "HEAD", fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	for name := range tr {
		if !fixtureScope().includes(name) {
			t.Errorf("%s read from the commit, out of scope", name)
		}
	}
	if _, ok := tr["internal/a/b.go"]; !ok {
		t.Errorf("internal/a/b.go not read from the head commit: %v", tr)
	}

	// A directory the commit does not have: nothing to read, no error.
	tr, err = commitTree(dir, "main", scope{dir: "absent/", exclude: regexp.MustCompile(defaultExclude)})
	if err != nil || len(tr) != 0 {
		t.Errorf("absent directory: %d files, err = %v, want none", len(tr), err)
	}

	if _, err := commitTree(dir, "no-such-commit", fixtureScope()); err == nil || !strings.Contains(err.Error(), "git ls-tree") {
		t.Errorf("unknown commit: err = %v, want the git error", err)
	}
}

// A symbolic link committed in scope is not followed: dirTree would not read
// it in the working tree either.
func TestCommitTreeSkipsSymlinks(t *testing.T) {
	dir := gitRepo(t)
	if err := os.Symlink("a.go", filepath.Join(dir, "internal", "a", "link.go")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "link")
	for name, read := range map[string]func() (tree, error){
		"commitTree": func() (tree, error) { return commitTree(dir, "HEAD", fixtureScope()) },
		"dirTree":    func() (tree, error) { return dirTree(dir, fixtureScope()) },
	} {
		tr, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := tr["internal/a/link.go"]; ok {
			t.Errorf("%s read a symbolic link", name)
		}
	}
}

func TestReadBatchObject(t *testing.T) {
	const object = "0123abcd"
	tests := map[string]struct {
		in      string
		want    string
		wantErr string
	}{
		"record":                {in: object + " blob 5\nhello\n", want: "hello"},
		"empty blob":            {in: object + " blob 0\n\n", want: ""},
		"content with newlines": {in: object + " blob 4\na\nb\n\nnext", want: "a\nb\n"},
		"no header":             {in: "", wantErr: "EOF"},
		"missing object":        {in: object + " missing\n", wantErr: "unexpected header"},
		"another object":        {in: "ffff blob 5\nhello\n", wantErr: "unexpected header"},
		"not a blob":            {in: object + " tree 5\nhello\n", wantErr: "unexpected header"},
		"size not a number":     {in: object + " blob five\nhello\n", wantErr: "invalid syntax"},
		"truncated content":     {in: object + " blob 5\nhel", wantErr: "EOF"},
		"no closing newline":    {in: object + " blob 5\nhelloX", wantErr: "not terminated by a newline"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := readBatchObject(bufio.NewReader(strings.NewReader(tc.in)), object)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Errorf("got %q, err = %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestDirTreeErrors(t *testing.T) {
	if _, err := dirTree(filepath.Join(t.TempDir(), "absent"), fixtureScope()); err == nil {
		t.Error("no error for a root that does not exist")
	}
	// The root exists, the checked directory does not.
	if _, err := dirTree(t.TempDir(), fixtureScope()); err == nil {
		t.Error("no error for a root without the checked directory")
	}
}

func TestFindMergeBase(t *testing.T) {
	dir := gitRepo(t)
	main := runGit(t, dir, "rev-parse", "main")
	for _, base := range []string{"main", main, "HEAD~1"} {
		if got, err := findMergeBase(dir, base); err != nil || got != main {
			t.Errorf("findMergeBase(%q) = %q, %v, want %q", base, got, err, main)
		}
	}
	if _, err := findMergeBase(dir, "--all"); err == nil || !strings.Contains(err.Error(), "is not a revision") {
		t.Errorf("a base that reads as an option: err = %v", err)
	}
}
