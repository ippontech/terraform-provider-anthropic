test {
  parallel = true
}

run "environment_archive_resource_archives_environment" {
  parallel = true
  module { source = "./examples/resources/environment_archive" }

  assert {
    condition     = output.environment_archive_id != ""
    error_message = "Expected environment_archive_id to be non-empty."
  }
  assert {
    condition     = output.archived_at != ""
    error_message = "Expected archived_at to be set."
  }
}
