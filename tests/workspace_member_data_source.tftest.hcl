test {
  parallel = true
}

run "workspace_member_data_source_reads_member" {
  command = plan

  module {
    source = "./examples/data-sources/anthropic_workspace_member"
  }

  variables {
    user_id = "user_placeholder"
  }
}
