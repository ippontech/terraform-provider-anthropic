// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Files are a standard-API (non-admin) resource scoped to the workspace of
// the ANTHROPIC_API_KEY used by the test, so they support full
// create/read/update/delete acceptance tests (unlike the admin-API resources
// blocked by #58). Uploading, listing, retrieving metadata for, and deleting
// files is free.

const (
	destroyCheckTimeout  = 5 * time.Second
	destroyCheckInterval = 200 * time.Millisecond
)

func newAccTestClient() anthropic.Client {
	return *acctest.NewAPIKeyClient()
}

func isNotFoundError(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// awaitFileGone polls GetMetadata until it returns 404 (destroyed) or the
// deadline passes.
func awaitFileGone(client anthropic.Client, id string) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	var lastErr error
	for {
		_, err := client.Files.GetMetadata(context.Background(), id)
		if isNotFoundError(err) {
			return nil
		}
		if err == nil {
			lastErr = fmt.Errorf("file %s still exists %v after destroy", id, destroyCheckTimeout)
		} else {
			lastErr = fmt.Errorf("checking file %s after destroy: %w", id, err)
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(destroyCheckInterval)
	}
}

func testAccCheckFileDestroyed(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_file" {
			continue
		}
		if err := awaitFileGone(client, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// writeFixtureFile writes content to a temp file and returns its path. The
// file lives in t.TempDir(), which is cleaned up automatically.
func writeFixtureFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("unable to write fixture file %q: %s", p, err)
	}
	return p
}

func testAccFileResourceBasicConfig(sourcePath string) string {
	return fmt.Sprintf(`
resource "anthropic_file" "test" {
  source_path = %q
}
`, sourcePath)
}

func TestAccFileResource_basic(t *testing.T) {
	sourcePath := writeFixtureFile(t, "tf-acc-test-file.txt", "tf acceptance test fixture content")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceBasicConfig(sourcePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_file.test", "id"),
					resource.TestCheckResourceAttr("anthropic_file.test", "source_path", sourcePath),
					resource.TestCheckResourceAttrSet("anthropic_file.test", "source_hash"),
					resource.TestCheckResourceAttr("anthropic_file.test", "filename", "tf-acc-test-file.txt"),
					resource.TestCheckResourceAttr("anthropic_file.test", "mime_type", "text/plain"),
					resource.TestCheckResourceAttr("anthropic_file.test", "size_bytes", fmt.Sprintf("%d", len("tf acceptance test fixture content"))),
					resource.TestCheckResourceAttrSet("anthropic_file.test", "created_at"),
					resource.TestCheckResourceAttr("anthropic_file.test", "downloadable", "false"),
				),
			},
			// ImportState — source_path and source_hash are local-only (the API does
			// not return the local path a file was uploaded from), so an import
			// leaves them null until the next apply repopulates them from config.
			{
				ResourceName:            "anthropic_file.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_path", "source_hash"},
			},
		},
	})
}

func testAccFileResourceMimeTypeConfig(sourcePath, mimeType string) string {
	return fmt.Sprintf(`
resource "anthropic_file" "test" {
  source_path = %q
  mime_type   = %q
}
`, sourcePath, mimeType)
}

func TestAccFileResource_explicitMimeType(t *testing.T) {
	// A .txt file whose detected MIME type would be "text/plain": force an
	// explicit mismatched value to confirm it's actually sent to the API and
	// echoed back, not silently dropped (which previously caused a
	// "Provider produced inconsistent result after apply" on the next plan).
	sourcePath := writeFixtureFile(t, "tf-acc-test-file-mime.txt", "not really json")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceMimeTypeConfig(sourcePath, "application/json"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_file.test", "mime_type", "application/json"),
				),
			},
		},
	})
}

func testAccFileResourceExpiresConfig(sourcePath string, expiresInSeconds int) string {
	return fmt.Sprintf(`
resource "anthropic_file" "test" {
  source_path         = %q
  expires_in_seconds  = %d
}
`, sourcePath, expiresInSeconds)
}

func TestAccFileResource_expiresInSeconds(t *testing.T) {
	sourcePath := writeFixtureFile(t, "tf-acc-test-file-expires.txt", "expiring content")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceExpiresConfig(sourcePath, 3600),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_file.test", "id"),
					resource.TestCheckResourceAttr("anthropic_file.test", "expires_in_seconds", "3600"),
					resource.TestCheckResourceAttrSet("anthropic_file.test", "expires_at"),
				),
			},
			{
				Config: testAccFileResourceExpiresConfig(sourcePath, 7200),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_file.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_file.test", "expires_in_seconds", "7200"),
				),
			},
		},
	})
}

func TestAccFileResource_sourcePathChangeForcesReplacement(t *testing.T) {
	sourcePathV1 := writeFixtureFile(t, "tf-acc-test-file-v1.txt", "version 1 content")
	sourcePathV2 := writeFixtureFile(t, "tf-acc-test-file-v2.txt", "version 2 content")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceBasicConfig(sourcePathV1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_file.test", "source_path", sourcePathV1),
					resource.TestCheckResourceAttr("anthropic_file.test", "filename", "tf-acc-test-file-v1.txt"),
				),
			},
			{
				Config: testAccFileResourceBasicConfig(sourcePathV2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_file.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_file.test", "source_path", sourcePathV2),
					resource.TestCheckResourceAttr("anthropic_file.test", "filename", "tf-acc-test-file-v2.txt"),
				),
			},
		},
	})
}
