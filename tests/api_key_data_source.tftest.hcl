test {
  parallel = true
}

run "api_key_data_source_validates_schema" {
  command = plan

  module { source = "./examples/data-sources/api_key" }
}
