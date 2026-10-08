test {
  parallel = true
}

# Applies for real: uploads a file, then reads its metadata back by ID.
# File operations are free.
run "file_data_source_reads_uploaded_file" {
  module {
    source = "../examples/data-sources/file"
  }

  assert {
    condition     = output.file_id == anthropic_file.example.id
    error_message = "Expected the data source to return the uploaded file's ID."
  }

  assert {
    condition     = output.file_filename == "example.txt"
    error_message = "Expected the data source to return the uploaded file's filename."
  }
}
