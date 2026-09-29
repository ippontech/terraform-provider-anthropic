test {
  parallel = true
}

run "invite_data_source_validates_schema" {
  command = plan

  module { source = "../examples/data-sources/invite" }
}
