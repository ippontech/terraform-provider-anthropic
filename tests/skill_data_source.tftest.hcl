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
  assert {
    condition     = output.skill.source == "custom"
    error_message = "Expected source to be custom."
  }
  assert {
    condition     = output.skill.type == "skill"
    error_message = "Expected type to be skill."
  }
  assert {
    condition     = output.skill.created_at != ""
    error_message = "Expected created_at to be non-empty."
  }
  assert {
    condition     = output.skill.latest_version != ""
    error_message = "Expected latest_version to be non-empty."
  }
}
