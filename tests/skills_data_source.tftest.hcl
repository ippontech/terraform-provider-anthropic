# Tests for the anthropic_skills data source example.
# Verifies that the data source returns a non-empty list of skills with expected attributes.

test {
  parallel = true
}

run "skills_data_source_returns_list" {
  module {
    source = "./examples/data-sources/skills"
  }

  assert {
    condition     = length(output.skills) > 0
    error_message = "Expected at least one skill."
  }

  assert {
    condition     = output.skills[0].id != ""
    error_message = "Expected the first skill's id to be non-empty."
  }

  assert {
    condition     = output.skills[0].source != ""
    error_message = "Expected the first skill's source to be non-empty."
  }

  assert {
    condition     = output.skills[0].created_at != ""
    error_message = "Expected the first skill's created_at to be non-empty."
  }
}
