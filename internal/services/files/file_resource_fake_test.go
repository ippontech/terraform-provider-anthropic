// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const fileAddr = "anthropic_file.test"

// fileID is the ID the fake gives to the n-th file it stores.
func fileID(n int) string { return fmt.Sprintf("file_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// writeFile writes content to name in dir and returns its path as the
// configuration and the state write it.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(p)
}

func fileConfig(sourcePath, extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
resource "anthropic_file" "test" {
  source_path = %q
%s}
`, sourcePath, extra)
}

// expectAction checks the action the step plans for anthropic_file.test and
// that the refresh after the apply plans nothing.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(fileAddr, action)
}

type fileState struct {
	id, sourcePath, content, filename, mimeType, createdAt, expiresAt string
	expiresIn                                                         int64
}

func expectFile(s fileState) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(fileAddr, tfjsonpath.New(attr), c)
	}
	expiresAt, expiresIn := knownvalue.Check(knownvalue.Null()), knownvalue.Check(knownvalue.Null())
	if s.expiresAt != "" {
		expiresAt, expiresIn = knownvalue.StringExact(s.expiresAt), knownvalue.Int64Exact(s.expiresIn)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(s.id)),
		check("source_path", knownvalue.StringExact(s.sourcePath)),
		check("source_hash", knownvalue.StringExact(sha(s.content))),
		check("filename", knownvalue.StringExact(s.filename)),
		check("mime_type", knownvalue.StringExact(s.mimeType)),
		check("size_bytes", knownvalue.Int64Exact(int64(len(s.content)))),
		check("created_at", knownvalue.StringExact(s.createdAt)),
		check("downloadable", knownvalue.Bool(false)),
		check("expires_in_seconds", expiresIn),
		check("expires_at", expiresAt),
	}
}

// checkNoFile is a CheckDestroy: no file may be left in the fake.
func (f *filesFake) checkNoFile(*terraform.State) error {
	if n := f.files.Len(); n != 0 {
		return fmt.Errorf("%d files left after destroy", n)
	}
	return nil
}

// TestFileResource_lifecycle uploads a file, imports it, replaces it when
// its local content changes at the same path, then when filename,
// mime_type and expires_in_seconds are set, and deletes it.
func TestFileResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)
	dir := t.TempDir()
	src := writeFile(t, dir, "notes.html", "<p>first draft</p>")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoFile,
		Steps: []resource.TestStep{
			{
				Config:           fileConfig(src, ""),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectFile(fileState{id: fileID(1), sourcePath: src, content: "<p>first draft</p>",
					filename: "notes.html", mimeType: "text/html; charset=utf-8", createdAt: at(1)}),
			},
			{
				Config:                  fileConfig(src, ""),
				ResourceName:            fileAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_path", "source_hash"},
			},
			{
				PreConfig:        func() { writeFile(t, dir, "notes.html", "<p>second draft</p>") },
				Config:           fileConfig(src, ""),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionReplace),
				ConfigStateChecks: expectFile(fileState{id: fileID(2), sourcePath: src, content: "<p>second draft</p>",
					filename: "notes.html", mimeType: "text/html; charset=utf-8", createdAt: at(2)}),
			},
			{
				Config:           fileConfig(src, "  filename = \"renamed.md\"\n  mime_type = \"text/markdown\"\n  expires_in_seconds = 3600\n"),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionReplace),
				ConfigStateChecks: expectFile(fileState{id: fileID(3), sourcePath: src, content: "<p>second draft</p>",
					filename: "renamed.md", mimeType: "text/markdown", createdAt: at(3), expiresAt: "2026-01-01T01:00:03Z", expiresIn: 3600}),
			},
		},
	})

	want := []string{
		"file=notes.html|<p>first draft</p>",
		"file=notes.html|<p>second draft</p>",
		"file=renamed.md|<p>second draft</p> expires_in_seconds=3600",
	}
	if got := srv.uploads(t); !slices.Equal(got, want) {
		t.Errorf("uploads %q, want %q", got, want)
	}
	srv.AssertCalled(routeDeleteFile, 3)
	srv.assertRequestShape(t)
}

// TestFileResource_removedOutsideTerraform: a file deleted behind
// Terraform's back is uploaded again; a 404 on the delete is success.
func TestFileResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)
	src := writeFile(t, t.TempDir(), "data.json", `{"a":1}`)
	config := fileConfig(src, "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.files.Delete(fileID(1)) },
				Config:           config,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectFile(fileState{id: fileID(2), sourcePath: src, content: `{"a":1}`,
					filename: "data.json", mimeType: "application/json", createdAt: at(2)}),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteFile, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertCalled(routeUploadFile, 2)
	srv.AssertCalled(routeDeleteFile, 1)
	srv.assertRequestShape(t)
}

// TestFileResource_errors: a missing local file fails before any upload;
// an API error on upload (not retried), refresh or delete reaches the
// operator; an invalid filename is refused at plan time.
func TestFileResource_errors(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)
	dir := t.TempDir()
	src := writeFile(t, dir, "doc.bin", "bytes")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoFile,
		Steps: []resource.TestStep{
			{
				Config:      fileConfig(filepath.ToSlash(filepath.Join(dir, "missing.bin")), ""),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+source_path.*Unable\s+to\s+access\s+file`),
			},
			{
				Config:      fileConfig(src, "  filename = \"a/b.bin\"\n"),
				ExpectError: regexp.MustCompile(`Invalid\s+filename`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUploadFile, http.StatusInternalServerError, 1) },
				Config:      fileConfig(src, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+upload\s+file:.*500`),
			},
			{
				Config:           fileConfig(src, ""),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectFile(fileState{id: fileID(1), sourcePath: src, content: "bytes",
					filename: "doc.bin", mimeType: "application/octet-stream", createdAt: at(1)}),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetFile, http.StatusForbidden, 1) },
				Config:      fileConfig(src, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+file:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteFile, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+file:.*400`),
			},
		},
	})

	// The 500 is not replayed (POST /v1/files is not idempotent): one
	// faulted upload, then the one that worked.
	srv.AssertCalled(routeUploadFile, 2)
	srv.AssertCalled(routeDeleteFile, 2)
	srv.assertRequestShape(t)
}
