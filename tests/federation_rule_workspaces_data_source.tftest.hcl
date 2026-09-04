test {
  parallel = true
}

run "federation_rule_workspaces_data_source_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_rule_workspaces_plan"
  }
}
