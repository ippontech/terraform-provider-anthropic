test {
  parallel = true
}

run "skill_version_data_source_returns_skill_version" {
  parallel = true
  module {
    source = "./examples/data-sources/skill_version"
  }

  assert {
    condition     = output.skill_version.id != ""
    error_message = "Expected skill_version id to be non-empty."
  }

  assert {
    condition     = output.skill_version.name != ""
    error_message = "Expected skill_version name to be non-empty."
  }

  assert {
    condition     = output.skill_version.version != ""
    error_message = "Expected skill_version version to be non-empty."
  }

  assert {
    condition     = output.skill_version.created_at != ""
    error_message = "Expected skill_version created_at to be non-empty."
  }

  assert {
    condition     = output.skill_version.skill_id == anthropic_skill_version.example.skill_id
    error_message = "Expected the data source skill_id to match the resource skill_id."
  }

  assert {
    condition     = output.skill_version.version == anthropic_skill_version.example.version
    error_message = "Expected the data source version to match the resource version."
  }
}
