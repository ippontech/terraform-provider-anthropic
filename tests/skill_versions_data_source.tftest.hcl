# Tests for the anthropic_skill_versions data source example.
# Verifies that the data source returns a non-empty list of skill versions with expected attributes.

test {
  parallel = true
}

run "skill_versions_data_source_returns_results" {
  module {
    source = "./examples/data-sources/skill_versions"
  }

  assert {
    condition     = length(output.skill_versions) > 0
    error_message = "Expected at least one skill version to be returned, but got none."
  }

  assert {
    condition     = output.skill_versions[0].id != ""
    error_message = "Expected the first skill version's id to be non-empty."
  }

  assert {
    condition     = output.skill_versions[0].version != ""
    error_message = "Expected the first skill version's version to be non-empty."
  }

  assert {
    condition     = output.skill_versions[0].created_at != ""
    error_message = "Expected the first skill version's created_at to be non-empty."
  }
}
