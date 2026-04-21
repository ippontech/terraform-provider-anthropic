test {
  parallel = true
}

run "skill_data_source_returns_skill" {
  parallel = true
  module {
    source = "./examples/data-sources/skill"
  }

  assert {
    condition     = output.skill.id != ""
    error_message = "Expected skill id to be non-empty."
  }
}
