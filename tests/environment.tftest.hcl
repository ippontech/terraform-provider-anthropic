test {
  parallel = true
}

run "environment_resource_creates_environment" {
  parallel = true
  module { source = "./examples/resources/environment" }

  assert {
    condition     = output.environment_id != ""
    error_message = "Expected environment_id to be non-empty."
  }
  assert {
    condition     = output.environment_type == "environment"
    error_message = "Expected type == \"environment\"."
  }
  assert {
    condition     = output.environment_networking_type == "limited"
    error_message = "Expected networking.type == \"limited\"."
  }
}
