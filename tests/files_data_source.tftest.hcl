test {
  parallel = true
}

# Applies for real: uploads a file, then lists it through the `ids` filter.
run "files_data_source_lists_uploaded_file" {
  module {
    source = "../examples/data-sources/files"
  }

  assert {
    condition     = length(output.file_ids) == 1
    error_message = "Expected exactly one file for a single-ID filter."
  }

  assert {
    condition     = output.file_ids[0] == anthropic_file.example.id
    error_message = "Expected the listed file to be the uploaded one."
  }
}
