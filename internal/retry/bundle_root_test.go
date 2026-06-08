// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveBundleRoot_SingleFileAtRoot(t *testing.T) {
	root, dirName, err := DeriveBundleRoot([]string{filepath.Join("bundle", "SKILL.md")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "bundle" {
		t.Errorf("root = %q, want %q", root, "bundle")
	}
	if dirName != "bundle" {
		t.Errorf("dirName = %q, want %q", dirName, "bundle")
	}
}

func TestDeriveBundleRoot_MultipleFilesFlat(t *testing.T) {
	root, dirName, err := DeriveBundleRoot([]string{
		filepath.Join("bundle", "SKILL.md"),
		filepath.Join("bundle", "extra.md"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "bundle" {
		t.Errorf("root = %q, want %q", root, "bundle")
	}
	if dirName != "bundle" {
		t.Errorf("dirName = %q, want %q", dirName, "bundle")
	}
}

func TestDeriveBundleRoot_IndependentOfOrder(t *testing.T) {
	bundle := "bundle"
	nested := filepath.Join(bundle, "Assets", "icon.png")
	skill := filepath.Join(bundle, "SKILL.md")

	rootA, nameA, errA := DeriveBundleRoot([]string{skill, nested})
	rootB, nameB, errB := DeriveBundleRoot([]string{nested, skill})

	if errA != nil || errB != nil {
		t.Fatalf("unexpected errors: %v / %v", errA, errB)
	}
	if rootA != rootB {
		t.Errorf("root depends on input order: %q vs %q", rootA, rootB)
	}
	if nameA != nameB {
		t.Errorf("dirName depends on input order: %q vs %q", nameA, nameB)
	}
	if rootA != bundle {
		t.Errorf("root = %q, want %q", rootA, bundle)
	}
}

func TestDeriveBundleRoot_DeeplyNestedFiles(t *testing.T) {
	root, dirName, err := DeriveBundleRoot([]string{
		filepath.Join("bundle", "SKILL.md"),
		filepath.Join("bundle", "references", "templates", "report.md"),
		filepath.Join("bundle", "references", "schemas", "bq.md"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "bundle" {
		t.Errorf("root = %q, want %q", root, "bundle")
	}
	if dirName != "bundle" {
		t.Errorf("dirName = %q, want %q", dirName, "bundle")
	}
}

func TestDeriveBundleRoot_AbsolutePaths(t *testing.T) {
	base := string(filepath.Separator) + filepath.Join("tmp", "bundle")
	root, dirName, err := DeriveBundleRoot([]string{
		filepath.Join(base, "SKILL.md"),
		filepath.Join(base, "references", "template.md"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != base {
		t.Errorf("root = %q, want %q", root, base)
	}
	if dirName != "bundle" {
		t.Errorf("dirName = %q, want %q", dirName, "bundle")
	}
}

func TestDeriveBundleRoot_NoCommonParent(t *testing.T) {
	_, _, err := DeriveBundleRoot([]string{
		filepath.Join("bundleA", "SKILL.md"),
		filepath.Join("bundleB", "SKILL.md"),
	})
	if err == nil {
		t.Fatal("expected error when files do not share a common parent")
	}
	if !strings.Contains(err.Error(), "common parent") {
		t.Errorf("expected 'common parent' in error, got %v", err)
	}
}

func TestDeriveBundleRoot_EmptyPaths(t *testing.T) {
	_, _, err := DeriveBundleRoot(nil)
	if err == nil {
		t.Fatal("expected error for empty filePaths")
	}
}
