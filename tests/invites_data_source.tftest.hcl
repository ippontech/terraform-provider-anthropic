test {
  parallel = true
}

run "invites_data_source_validates_schema" {
  command = plan

  module { source = "../examples/data-sources/invites" }
}
