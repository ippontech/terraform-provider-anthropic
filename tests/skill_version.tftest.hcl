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

  assert {
    condition     = output.name != ""
    error_message = "Expected name to be non-empty."
  }

  assert {
    condition     = output.created_at != ""
    error_message = "Expected created_at to be non-empty."
  }
}
