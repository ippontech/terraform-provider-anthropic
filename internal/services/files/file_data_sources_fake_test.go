// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/files"
)

// threeFiles stores three files, the second one expiring after an hour.
func (f *filesFake) threeFiles() {
	f.newFile("a.pdf", "application/pdf", 10, 0)
	f.newFile("b.png", "image/png", 20, 3600)
	f.newFile("c.txt", "text/plain", 30, 0)
}

func fileObject(n int, filename, mimeType string, size int64, expiresAt string) map[string]knownvalue.Check {
	expires := knownvalue.Check(knownvalue.Null())
	if expiresAt != "" {
		expires = knownvalue.StringExact(expiresAt)
	}
	return map[string]knownvalue.Check{
		"id": knownvalue.StringExact(fileID(n)), "filename": knownvalue.StringExact(filename), "mime_type": knownvalue.StringExact(mimeType),
		"size_bytes": knownvalue.Int64Exact(size), "created_at": knownvalue.StringExact(at(n)), "downloadable": knownvalue.Bool(false),
		"expires_at": expires, "type": knownvalue.StringExact("file"),
	}
}

func dataChecks(addr string, attrs map[string]knownvalue.Check) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for name, c := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), c))
	}
	return checks
}

// TestFileDataSource_read reads a file's metadata, expires_at included; an
// unknown ID is a File Not Found error and another API error a client
// error.
func TestFileDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)
	srv.threeFiles()
	const addr = "data.anthropic_file.test"
	block := func(id string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_file" "test" { id = "` + id + `" }`
	}

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{Config: block(fileID(2)), ConfigStateChecks: dataChecks(addr, fileObject(2, "b.png", "image/png", 20, "2026-01-01T01:00:02Z"))},
			{Config: block(fileID(1)), ConfigStateChecks: dataChecks(addr, fileObject(1, "a.pdf", "application/pdf", 10, ""))},
			{Config: block("file_missing"), ExpectError: regexp.MustCompile(`(?s)File\s+Not\s+Found.*"file_missing"`)},
			{
				PreConfig:   func() { srv.FailNext(routeGetFile, http.StatusForbidden, 1) },
				Config:      block(fileID(3)),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+file:.*403`),
			},
		},
	})
	srv.AssertWalks(routeGetFile, "")
	srv.assertRequestShape(t)
}

// TestFilesDataSource_pages lists every file over two pages, 1000 per page
// asked.
func TestFilesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 2)
	srv.threeFiles()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_files" "test" {}`,
			ConfigStateChecks: dataChecks("data.anthropic_files.test", map[string]knownvalue.Check{
				"ids": knownvalue.Null(),
				"files": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(fileObject(1, "a.pdf", "application/pdf", 10, "")),
					knownvalue.ObjectExact(fileObject(2, "b.png", "image/png", 20, "2026-01-01T01:00:02Z")),
					knownvalue.ObjectExact(fileObject(3, "c.txt", "text/plain", 30, "")),
				}),
			}),
		}},
	})
	srv.AssertWalks(routeListFiles, "limit=1000", "limit=1000&page="+sdktest.NextPageToken(fileID(2)))
	srv.AssertAllPagesRead(routeListFiles)
	srv.assertRequestShape(t)
}

// TestFilesDataSource_ids sends ids[] without limit or page and gets the
// known files only.
func TestFilesDataSource_ids(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)
	srv.threeFiles()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_files" "test" { ids = ["` + fileID(3) + `", "file_gone", "` + fileID(1) + `"] }`,
			ConfigStateChecks: dataChecks("data.anthropic_files.test", map[string]knownvalue.Check{
				"files": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(fileObject(1, "a.pdf", "application/pdf", 10, "")),
					knownvalue.ObjectExact(fileObject(3, "c.txt", "text/plain", 30, "")),
				}),
			}),
		}},
	})
	srv.AssertWalks(routeListFiles, "ids%5B%5D="+fileID(3)+"&ids%5B%5D=file_gone&ids%5B%5D="+fileID(1))
}

// TestFilesDataSource_emptyAndError: no file is an empty list, never null; a
// failed list is an error.
func TestFilesDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newFilesFake(t, 0)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_files" "test" {}`,
				ConfigStateChecks: dataChecks("data.anthropic_files.test", map[string]knownvalue.Check{
					"files": knownvalue.ListExact([]knownvalue.Check{}),
				}),
			},
			{
				PreConfig:   func() { srv.FailNext(routeListFiles, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_files" "other" {}`,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+files:.*403`),
			},
		},
	})
}

// TestFiles_requireAPIKey: the resource and both data sources refuse to run
// without the standard key, before any request.
func TestFiles_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_file resource": `resource "anthropic_file" "test" { source_path = "x" }`,
		"anthropic_file":          `data "anthropic_file" "test" { id = "file_x" }`,
		"anthropic_files":         `data "anthropic_files" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFilesFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey, sdktest.AuthToken) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestFiles_rejectForeignProviderData covers the Configure guard Terraform
// cannot reach: provider data of another type is an error.
func TestFiles_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	files.NewFileResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_file: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_file":  files.NewFileDataSource(),
		"anthropic_files": files.NewFilesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
