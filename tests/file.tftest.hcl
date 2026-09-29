test {
  parallel = true
}

# Applies for real (apply is the default command): file uploads, downloads,
# listing, metadata reads, and deletes are all free, so this exercises the
# full create/read/destroy path against the live API.
run "file_resource_uploads_file" {
  module {
    source = "../examples/resources/file"
  }

  assert {
    condition     = output.file_id != ""
    error_message = "Expected file_id to be non-empty."
  }
}
