test {
  parallel = true
}

run "service_account_plan" {
  command = plan

  module {
    source = "./examples/resources/service_account"
  }
}
