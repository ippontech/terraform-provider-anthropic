// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func TestAccFileDataSource_basic(t *testing.T) {
	content := "tf acceptance test fixture content"
	sourcePath := writeFixtureFile(t, "tf-acc-test-file-ds.txt", content)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceBasicConfig(sourcePath) + `
data "anthropic_file" "test" {
  id = anthropic_file.test.id
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.anthropic_file.test", "id", "anthropic_file.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_file.test", "filename", "tf-acc-test-file-ds.txt"),
					resource.TestCheckResourceAttr("data.anthropic_file.test", "mime_type", "text/plain"),
					resource.TestCheckResourceAttr("data.anthropic_file.test", "size_bytes", fmt.Sprintf("%d", len(content))),
					resource.TestCheckResourceAttrSet("data.anthropic_file.test", "created_at"),
					resource.TestCheckResourceAttr("data.anthropic_file.test", "downloadable", "false"),
					resource.TestCheckNoResourceAttr("data.anthropic_file.test", "expires_at"),
					resource.TestCheckResourceAttr("data.anthropic_file.test", "type", "file"),
				),
			},
		},
	})
}
