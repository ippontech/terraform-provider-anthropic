// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command coversummary turns a Go coverage profile into a Markdown summary
// and, with -config, enforces the coverage gate of the CI coverage job.
//
// The summary is the total, then one row per file matching -match (by default
// every resource and data source file), least covered first.
//
// The gate (gate.go) reads its thresholds and exclusions from .coverage.json
// and the committed baseline from .coverage-baseline. It has four rules:
//
//   - diff: at least diff_min % of the executable statements a change touches
//     (non-test Go files, outside the exclusions) are covered; it needs the
//     output of `git diff --unified=0 <base>...HEAD` (-diff);
//   - ratchet: the total stays at or above the baseline minus
//     ratchet_tolerance;
//   - floor: once the baseline reaches total_target, the total may never drop
//     below total_target, and the baseline may not be lowered under it
//     (-base-baseline gives the base branch's baseline);
//   - packages: every package outside the exclusions is at package_min % or
//     more. While package_min is null, the rule is not enforced: the summary
//     still lists every package with its distance to total_target, least
//     covered first.
//
// It prints the summary on stdout and exits 1 when a rule fails, 2 on a usage
// or input error.
//
//	go test -coverprofile=coverage.out ./...
//	git diff --unified=0 origin/main...HEAD | go run -C tools ./coversummary -config ../.coverage.json -diff - ../coverage.out
//
// It uses the standard library only, so it adds nothing to the tools module.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// errGateFailed reports that a rule of the gate failed; the summary says
// which.
var errGateFailed = errors.New("coverage gate failed")

type options struct {
	match        *regexp.Regexp
	trim         string
	configPath   string
	diffPath     string
	baseBaseline string
}

func main() {
	match := flag.String("match", `_(resource|data_source)\.go$`, "regular expression selecting the files listed one by one")
	trim := flag.String("trim", "github.com/ippontech/terraform-provider-anthropic/", "module path prefix removed from the file paths of the profile")
	configPath := flag.String("config", "", "coverage gate configuration (.coverage.json); without it, only the summary is printed")
	diffPath := flag.String("diff", "", "output of git diff --unified=0 <base>...HEAD for the diff rule, - for stdin")
	baseBaseline := flag.String("base-baseline", "", "the base branch's .coverage-baseline, for the floor rule (a missing file is skipped)")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: coversummary [-match regexp] [-trim prefix] [-config .coverage.json [-diff file] [-base-baseline file]] coverage.out")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	re, err := regexp.Compile(*match)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coversummary: invalid -match: %v\n", err)
		os.Exit(2)
	}
	opts := options{match: re, trim: *trim, configPath: *configPath, diffPath: *diffPath, baseBaseline: *baseBaseline}
	switch err := run(os.Stdout, os.Stdin, flag.Arg(0), opts); {
	case errors.Is(err, errGateFailed):
		fmt.Fprintf(os.Stderr, "coversummary: %v (details in the summary)\n", err)
		os.Exit(1)
	case err != nil:
		fmt.Fprintf(os.Stderr, "coversummary: %v\n", err)
		os.Exit(2)
	}
}

func run(w io.Writer, stdin io.Reader, profilePath string, opts options) error {
	f, err := os.Open(filepath.Clean(profilePath))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	files, err := parseProfile(f)
	if err != nil {
		return fmt.Errorf("%s: %w", profilePath, err)
	}
	if opts.configPath == "" {
		return writeSummary(w, files, opts.match, opts.trim, nil)
	}

	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	in := gateInput{config: cfg}
	if in.baseline, err = readBaseline(cfg.baselinePath); err != nil {
		return err
	}
	if opts.baseBaseline != "" {
		if in.baseBaseline, err = readOptionalBaseline(opts.baseBaseline); err != nil {
			return err
		}
	}
	if opts.diffPath != "" {
		r := stdin
		if opts.diffPath != "-" {
			df, err := os.Open(filepath.Clean(opts.diffPath))
			if err != nil {
				return err
			}
			defer func() { _ = df.Close() }()
			r = df
		}
		if in.changes, err = parseDiff(r); err != nil {
			return fmt.Errorf("%s: %w", opts.diffPath, err)
		}
	}

	result := evaluate(in, files, opts.trim)
	if err := writeGate(w, result); err != nil {
		return err
	}
	if err := writeSummary(w, files, opts.match, opts.trim, cfg.excluded); err != nil {
		return err
	}
	if !result.passed() {
		return errGateFailed
	}
	return nil
}
