// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DeriveBundleRoot returns the longest shared parent of filePaths and its
// base name. Using the common parent (rather than filepath.Dir(filePaths[0]))
// keeps the result independent of input order — necessary because
// `fileset()` returns lexicographically sorted paths and a nested file may
// sort before SKILL.md.
func DeriveBundleRoot(filePaths []string) (root, dirName string, err error) {
	if len(filePaths) == 0 {
		return "", "", fmt.Errorf("DeriveBundleRoot: no file paths provided")
	}

	root = filepath.Dir(filePaths[0])
	for _, p := range filePaths[1:] {
		root = commonPathPrefix(root, filepath.Dir(p))
		if root == "" {
			return "", "", fmt.Errorf("DeriveBundleRoot: files do not share a common parent directory: %q vs %q", filePaths[0], p)
		}
	}

	dirName = filepath.Base(root)
	return root, dirName, nil
}

// commonPathPrefix returns the longest shared parent of a and b, trimmed at
// a path-segment boundary. Returns "" if they share no segments (e.g.
// different Windows volumes, or one absolute and one relative).
func commonPathPrefix(a, b string) string {
	if a == b {
		return a
	}
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	sep := string(filepath.Separator)
	aSegs := strings.Split(a, sep)
	bSegs := strings.Split(b, sep)
	n := len(aSegs)
	if len(bSegs) < n {
		n = len(bSegs)
	}
	common := 0
	for common < n && aSegs[common] == bSegs[common] {
		common++
	}
	if common == 0 {
		return ""
	}
	return strings.Join(aSegs[:common], sep)
}
