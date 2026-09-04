test {
  parallel = true
}

run "federation_issuers_data_source_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_issuers_plan"
  }
}
