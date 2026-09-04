test {
  parallel = true
}

# The federation admin endpoints require an org:admin OAuth bearer token and
# reject API keys outright (see internal/errors/auth_token.go). This fixture
# defers the data source's read past plan (see
# tests/fixtures/federation_issuer_data_source_plan/main.tf for how), so
# command = plan validates the schema without ever making a live API call
# with the fixture's dummy auth_token.
run "federation_issuer_data_source_validates_schema" {
  command = plan

  module { source = "./tests/fixtures/federation_issuer_data_source_plan" }
}
