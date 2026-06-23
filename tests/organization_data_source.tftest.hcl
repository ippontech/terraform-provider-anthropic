test {
  parallel = true
}

run "organization_data_source_reads_organization" {
  command = plan

  module {
    source = "./examples/data-sources/organization"
  }
}
