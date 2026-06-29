test {
  parallel = true
}

run "organization_member_data_source_validates_schema" {
  command = plan

  module { source = "./examples/data-sources/organization_member" }
}
