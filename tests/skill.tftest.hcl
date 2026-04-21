test {
  parallel = true
}

run "skill_resource_creates_skill" {
  parallel = true
  module {
    source = "./examples/resources/skill"
  }

  assert {
    condition     = output.skill_id != ""
    error_message = "Expected skill_id to be non-empty."
  }
}
