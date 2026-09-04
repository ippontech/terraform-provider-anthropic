test {
  parallel = true
}

run "federation_rule_workspaces_data_source_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_rule_workspaces_plan"
  }

  assert {
    condition     = output.federation_rule_id == "fdrl_01ABCDEFGHIJKLMNOPQRSTUV"
    error_message = "Expected federation_rule_id to round-trip through the data source config."
  }
}
