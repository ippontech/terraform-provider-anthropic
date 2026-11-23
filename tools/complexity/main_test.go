// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitRepo commits testdata/base on main, then testdata/head on a branch
// "change" that it leaves checked out, and returns the repository.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	// No user or system configuration (signing, hooks, default branch).
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	copyTree(t, filepath.Join("testdata", "base"), dir)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")
	runGit(t, dir, "checkout", "-q", "-b", "change")
	if err := os.RemoveAll(filepath.Join(dir, "internal")); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("testdata", "head"), dir)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "change")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		return os.WriteFile(dst, content, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureOptions(dir, base string) options {
	return options{base: base, root: dir, limit: fixtureLimit, dir: "internal/", exclude: regexp.MustCompile(defaultExclude)}
}

func TestRunAgainstGit(t *testing.T) {
	dir := gitRepo(t)
	mergeBase := runGit(t, dir, "rev-parse", "main")

	var out strings.Builder
	err := run(&out, fixtureOptions(dir, "main"))
	if !errors.Is(err, errRatchetFailed) {
		t.Fatalf("run against main: err = %v, want errRatchetFailed", err)
	}
	for _, w := range []string{
		"| A new function scores 3 or less | 2 above | **FAIL** |",
		"8 functions score above 3 at the merge base (`" + mergeBase[:12] + "`).",
		" | - | 5 | gone: deleted, renamed or moved to another package |",
		"Scope: 13 functions",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}

	// Against HEAD itself, the merge base is the working tree's commit: the
	// files out of scope (test, testdata, sdktest, outside internal/) must be
	// left out of the merge base too, or they would show up as gone.
	out.Reset()
	if err := run(&out, fixtureOptions(dir, "HEAD")); err != nil {
		t.Fatalf("run against HEAD: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "All rules pass.") || strings.Contains(out.String(), "gone") {
		t.Errorf("run against HEAD:\n%s", out.String())
	}

	// The working tree counts, committed or not.
	if err := os.WriteFile(filepath.Join(dir, "internal", "a", "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(&out, fixtureOptions(dir, "HEAD")); err != nil {
		t.Fatalf("run against HEAD after an edit: %v", err)
	}
	if !strings.Contains(out.String(), "| `internal/a.Untouched` | `internal/a/a.go:11` | - | 5 | gone") {
		t.Errorf("an uncommitted deletion is not seen:\n%s", out.String())
	}
}

func TestRunInputErrors(t *testing.T) {
	dir := gitRepo(t)
	for _, base := range []string{"--output=x", "no-such-branch"} {
		var out strings.Builder
		err := run(&out, fixtureOptions(dir, base))
		if err == nil || errors.Is(err, errRatchetFailed) {
			t.Errorf("base %q: err = %v, want an input error", base, err)
		}
	}
}

// TestRunErrors covers what stops run after the merge base is found.
func TestRunErrors(t *testing.T) {
	const invalid = "package a\nfunc {"
	file := filepath.Join("internal", "a", "a.go")
	tests := map[string]struct {
		prepare func(t *testing.T, dir string)
		want    string
	}{
		"working tree without the checked directory": {
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(dir, "internal")); err != nil {
					t.Fatal(err)
				}
			},
			want: "internal",
		},
		"working tree file that does not parse": {
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, file), []byte(invalid), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "internal/a/a.go:2",
		},
		"merge base file that does not parse": {
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, file), []byte(invalid), 0o600); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "commit", "-q", "-a", "-m", "invalid")
			},
			want: "merge base ",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := gitRepo(t)
			tc.prepare(t, dir)
			err := run(io.Discard, fixtureOptions(dir, "HEAD"))
			if err == nil || errors.Is(err, errRatchetFailed) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want an input error naming %q", err, tc.want)
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRunReportsWriteError(t *testing.T) {
	err := run(failingWriter{}, fixtureOptions(gitRepo(t), "HEAD"))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v, want the write error", err)
	}
}

// Without -root, the repository is the one holding the current directory,
// from any of its subdirectories.
func TestRunFindsTheRepositoryRoot(t *testing.T) {
	dir := gitRepo(t)
	opts := fixtureOptions("", "HEAD")

	t.Chdir(filepath.Join(dir, "internal", "a"))
	var out strings.Builder
	if err := run(&out, opts); err != nil {
		t.Fatalf("run from a subdirectory: %v", err)
	}
	if !strings.Contains(out.String(), "Scope: 13 functions") {
		t.Errorf("run from a subdirectory:\n%s", out.String())
	}

	outside := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(outside))
	t.Chdir(outside)
	if err := run(io.Discard, opts); err == nil || !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("run outside a repository: err = %v, want the git error", err)
	}
}

func TestCLI(t *testing.T) {
	dir := gitRepo(t)
	const usage = "usage: complexity -base <ref>"
	tests := map[string]struct {
		args       []string
		code       int
		wantStdout string
		wantStderr string
	}{
		"rule failed":           {args: []string{"-base", "main", "-root", dir, "-max", "3"}, code: 1, wantStdout: "**FAIL**", wantStderr: "complexity ratchet failed (details in the report)"},
		"passes":                {args: []string{"-base", "HEAD", "-root", dir, "-max", "3"}, code: 0, wantStdout: "All rules pass."},
		"-max is the cap":       {args: []string{"-base", "main", "-root", dir, "-max", "5"}, code: 0, wantStdout: "All rules pass."},
		"default cap of 20":     {args: []string{"-base", "main", "-root", dir}, code: 0, wantStdout: "A new function scores 20 or less"},
		"-dir is the scope":     {args: []string{"-base", "HEAD", "-root", dir, "-dir", "cmd/"}, code: 0, wantStdout: "Scope: 2 functions"},
		"-exclude leaves out":   {args: []string{"-base", "HEAD", "-root", dir, "-exclude", "^internal/a/"}, code: 0, wantStdout: "Scope: 1 functions"},
		"unknown base":          {args: []string{"-base", "no-such-branch", "-root", dir}, code: 2, wantStderr: "complexity: git merge-base"},
		"no -base":              {args: []string{"-root", dir}, code: 2, wantStderr: usage},
		"-max below 1":          {args: []string{"-base", "main", "-max", "0"}, code: 2, wantStderr: usage},
		"-dir without a slash":  {args: []string{"-base", "main", "-dir", "internal"}, code: 2, wantStderr: usage},
		"stray argument":        {args: []string{"-base", "main", "extra"}, code: 2, wantStderr: usage},
		"unknown flag":          {args: []string{"-nope"}, code: 2, wantStderr: usage},
		"-max not a number":     {args: []string{"-base", "main", "-max", "x"}, code: 2, wantStderr: usage},
		"invalid -exclude":      {args: []string{"-base", "main", "-exclude", "("}, code: 2, wantStderr: "complexity: invalid -exclude"},
		"help is not a failure": {args: []string{"-h"}, code: 0, wantStderr: usage},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if code := cli(tc.args, &stdout, &stderr); code != tc.code {
				t.Errorf("exit code %d, want %d\nstderr: %s", code, tc.code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout lacks %q:\n%s", tc.wantStdout, stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr lacks %q:\n%s", tc.wantStderr, stderr.String())
			}
			if tc.code == 2 && stdout.Len() != 0 {
				t.Errorf("a usage or input error wrote to stdout:\n%s", stdout.String())
			}
		})
	}
}

func TestModuleVersion(t *testing.T) {
	if v := moduleVersion("example.com/not/a/dependency"); v != "" {
		t.Errorf("version %q for a module that is not compiled in", v)
	}
}
