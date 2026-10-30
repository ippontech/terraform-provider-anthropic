// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command complexity is the cognitive complexity ratchet of the CI complexity
// job (`make complexity` locally).
//
// It scores every function declared in the non-test Go files under internal/
// (the test-helper packages and generated files aside) twice: in the working
// tree, and at the merge base of HEAD with -base. Then:
//
//   - a function that scores above -max at the merge base may change, but its
//     score may not rise;
//   - every other function, new or not, scores -max or less.
//
// Nothing is committed: the functions allowed above the cap are those above it
// on the base branch, so once none is left there the cap is absolute.
//
// Scores are gocognit's (github.com/uudashr/gocognit, the version
// golangci-lint is built with), computed file by file as golangci-lint's
// gocognit linter does. The //gocognit:ignore directive and //nolint
// comments exempt nothing here.
//
// It prints a Markdown report on stdout and exits 1 when a rule fails, 2 on a
// usage or input error.
//
//	go run -C tools ./complexity -base origin/main
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
)

// errRatchetFailed reports that a rule failed; the report says which.
var errRatchetFailed = errors.New("complexity ratchet failed")

// defaultExclude matches the test-helper packages, as the gocognit exclusion
// of .golangci.yml does: they are test code.
const defaultExclude = `^internal/(acctest|wifprobetest|admintest|oauthtest|providertest|schematest|sdktest)/`

type options struct {
	base    string
	root    string // repository root; "" for the top level of the current one
	limit   int
	dir     string
	exclude *regexp.Regexp
}

func main() {
	base := flag.String("base", "", "base branch or commit; the ratchet compares with the merge base of HEAD and it (required)")
	root := flag.String("root", "", "repository root (default: the top level of the repository holding the current directory)")
	limit := flag.Int("max", 20, "cognitive complexity cap")
	dir := flag.String("dir", "internal/", "module-relative directory checked")
	exclude := flag.String("exclude", defaultExclude, "regular expression matching the module-relative paths left out")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: complexity -base <ref> [-root dir] [-max n] [-dir dir/] [-exclude regexp]")
		flag.PrintDefaults()
	}
	flag.Parse()
	re, err := regexp.Compile(*exclude)
	if err != nil {
		fmt.Fprintf(os.Stderr, "complexity: invalid -exclude: %v\n", err)
		os.Exit(2)
	}
	if flag.NArg() != 0 || *base == "" || *limit < 1 || !strings.HasSuffix(*dir, "/") {
		flag.Usage()
		os.Exit(2)
	}
	opts := options{base: *base, root: *root, limit: *limit, dir: *dir, exclude: re}
	switch err := run(os.Stdout, opts); {
	case errors.Is(err, errRatchetFailed):
		fmt.Fprintf(os.Stderr, "complexity: %v (details in the report)\n", err)
		os.Exit(1)
	case err != nil:
		fmt.Fprintf(os.Stderr, "complexity: %v\n", err)
		os.Exit(2)
	}
}

func run(w io.Writer, opts options) error {
	root := opts.root
	if root == "" {
		top, err := git("", "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		root = strings.TrimSpace(string(top))
	}
	sc := scope{dir: opts.dir, exclude: opts.exclude}
	mergeBase, err := findMergeBase(root, opts.base)
	if err != nil {
		return err
	}
	baseTree, err := commitTree(root, mergeBase, sc)
	if err != nil {
		return err
	}
	headTree, err := dirTree(root, sc)
	if err != nil {
		return err
	}
	base, err := scoreTree(baseTree)
	if err != nil {
		return fmt.Errorf("merge base %.12s: %w", mergeBase, err)
	}
	head, err := scoreTree(headTree)
	if err != nil {
		return err
	}
	r := evaluate(base, head, opts.limit)
	rep := report{mergeBase: mergeBase[:min(12, len(mergeBase))], dir: opts.dir, exclude: opts.exclude.String(), gocognit: moduleVersion("github.com/uudashr/gocognit")}
	if err := writeReport(w, r, rep); err != nil {
		return err
	}
	if !r.passed() {
		return errRatchetFailed
	}
	return nil
}

// moduleVersion returns the version of a dependency compiled in, "" when the
// build information does not say.
func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			return dep.Version
		}
	}
	return ""
}
