test {
  parallel = true
}

run "skill_version_resource_creates_version" {
  parallel = true
  module {
    source = "./examples/resources/skill_version"
  }

  assert {
    condition     = output.skill_version_id != ""
    error_message = "Expected skill_version_id to be non-empty."
  }

  assert {
    condition     = output.version != ""
    error_message = "Expected version to be non-empty."
  }
}
