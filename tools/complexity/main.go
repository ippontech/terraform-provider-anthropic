// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command complexity is the cognitive complexity ratchet of the CI complexity
// job (`make complexity` locally).
//
// It scores every function declared in the non-test Go files under internal/
// (the test-helper packages aside) twice: in the working tree, and at the
// merge base of HEAD with -base. Then:
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
// gocognit linter does. The //gocognit:ignore directive, //nolint comments and
// a "// Code generated ... DO NOT EDIT." header exempt nothing here: the
// repository generates no Go file, and code that must stay out of the ratchet
// goes through -exclude.
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
	os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

// cli runs the command with its arguments and returns its exit code.
func cli(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("complexity", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := flags.String("base", "", "base branch or commit; the ratchet compares with the merge base of HEAD and it (required)")
	root := flags.String("root", "", "repository root (default: the top level of the repository holding the current directory)")
	limit := flags.Int("max", 20, "cognitive complexity cap")
	dir := flags.String("dir", "internal/", "module-relative directory checked")
	exclude := flags.String("exclude", defaultExclude, "regular expression matching the module-relative paths left out")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: complexity -base <ref> [-root dir] [-max n] [-dir dir/] [-exclude regexp]")
		flags.PrintDefaults()
	}
	switch err := flags.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		return 2
	}
	re, err := regexp.Compile(*exclude)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "complexity: invalid -exclude: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 || *base == "" || *limit < 1 || !strings.HasSuffix(*dir, "/") {
		flags.Usage()
		return 2
	}
	opts := options{base: *base, root: *root, limit: *limit, dir: *dir, exclude: re}
	switch err := run(stdout, opts); {
	case errors.Is(err, errRatchetFailed):
		_, _ = fmt.Fprintf(stderr, "complexity: %v (details in the report)\n", err)
		return 1
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "complexity: %v\n", err)
		return 2
	}
	return 0
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
