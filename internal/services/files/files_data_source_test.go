// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func TestAccFilesDataSource_basic(t *testing.T) {
	sourcePath := writeFixtureFile(t, "tf-acc-test-files-ds.txt", "tf acceptance test fixture content")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFileDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFileResourceBasicConfig(sourcePath) + `
data "anthropic_files" "all" {
  depends_on = [anthropic_file.test]
}

data "anthropic_files" "filtered" {
  ids = [anthropic_file.test.id]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The unfiltered listing covers the whole workspace: assert the
					// uploaded file is among the entries, not an exact count.
					resource.TestCheckTypeSetElemNestedAttrs("data.anthropic_files.all", "files.*", map[string]string{
						"filename": "tf-acc-test-files-ds.txt",
					}),
					resource.TestCheckResourceAttr("data.anthropic_files.filtered", "files.#", "1"),
					resource.TestCheckResourceAttrPair("data.anthropic_files.filtered", "files.0.id", "anthropic_file.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_files.filtered", "files.0.filename", "tf-acc-test-files-ds.txt"),
				),
			},
		},
	})
}
