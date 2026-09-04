test {
  parallel = true
}

# Plan-only: the federation rule endpoints require a real org:admin OAuth
# bearer token, which is not available in CI. The fixture defers the data
# source read past plan (see tests/fixtures/federation_rule_data_source_plan/main.tf), so
# this only validates the schema.
run "federation_rule_data_source_validates_schema" {
  command = plan

  module { source = "./tests/fixtures/federation_rule_data_source_plan" }
}
