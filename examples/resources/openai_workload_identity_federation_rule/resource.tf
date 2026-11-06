resource "openai_workload_identity_provider" "github" {
  name     = "GitHub Actions"
  issuer   = "https://token.actions.githubusercontent.com"
  audience = "openai"
}

resource "openai_project" "example" {
  name = "Example"
}

resource "openai_project_service_account" "ci" {
  project_id = openai_project.example.id
  name       = "ci"
}

resource "openai_workload_identity_federation_rule" "ci" {
  workload_identity_provider_id = openai_workload_identity_provider.github.id
  project_id                    = openai_project.example.id
  service_account_id            = openai_project_service_account.ci.id
  name                          = "Release workflow"

  attribute_matchers = jsonencode({
    sub = "repo:example/repository:ref:refs/heads/main"
  })
}
