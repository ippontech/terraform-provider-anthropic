test {
  parallel = true
}

run "api_keys_data_source_validates_schema" {
  command = plan

  module { source = "./examples/data-sources/api_keys" }
}
