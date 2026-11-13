# Runtime identity example

Supply `environment` and the existing environment's IDs through a reviewed `.tfvars` file or workspace inputs. Keep provisioning credentials in the trusted environment, never in these files or runtime outputs. See the [permissions guide](../../docs/guides/runtime-permissions.md) before choosing scopes or migrating existing accounts.

`plan.txt` is an actual local development-provider plan using dummy credentials and empty local state. It is not a live vendor authorisation test or an apply. It can be reproduced with a locally built provider and a `provider_installation.dev_overrides` entry for the source in `main.tf`, then `terraform plan -refresh=false` with the required inputs. No backend or live identity is needed for that example plan. Production use requires the published private provider, registry authentication and a reviewed saved plan.
