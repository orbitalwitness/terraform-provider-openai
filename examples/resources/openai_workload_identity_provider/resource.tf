resource "openai_workload_identity_provider" "github" {
  name     = "GitHub Actions"
  issuer   = "https://token.actions.githubusercontent.com"
  audience = "openai"
}
