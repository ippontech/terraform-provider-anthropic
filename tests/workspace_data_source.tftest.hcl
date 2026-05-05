test {
  parallel = true
}

run "workspace_data_source_reads_workspace" {
  command = plan

  module { source = "./examples/data-sources/workspace" }
}
