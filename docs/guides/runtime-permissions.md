# Runtime identity permissions

Verified against current vendor documentation on 13 November 2026. This fork is not the official `openai/openai` provider; use this fork's resource and attribute names.

## Controls and boundaries

| Control | Selected value | Purpose / limitation |
| --- | --- | --- |
| Account creation | `create_service_account_only = true` | Creates `role=none`, without an initial key. Does not grant inference by itself. |
| Custom project role | `api.responses.write` | Responses creation, including streaming and function-tool requests. |
| Optional model capability | `api.model.request` | Chat Completions fallback and model requests, including audio. This is broader than Responses alone. |
| Decisions inference | `api.decisions.write` | Explicit Decisions API utility tasks in the current application; not covered merely by Responses access. |
| Role assignment | One project role through a dedicated group | Grants permissions to the identity itself, independently of WIF tokens. No built-in member/owner, organisation roles, billing, key or account management granted. |
| WIF mapping | Exact reviewed claims, optionally restricted scopes | Additional token ceiling; cannot enlarge account access. Mapping setup is separate from the example. |
| Admin credential | `OPENAI_ADMIN_KEY` in provisioning only | Manages accounts, roles, groups and assignments. Never distribute it to the runtime. |

The [creation API](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/service_accounts/methods/create) supports role-free, keyless creation. The [update API](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/service_accounts/methods/update) supports name and built-in `member`/`owner` roles, **not `none`**. This enhancement exposes the creation mode, not a fictitious role downgrade. It also removes the unnecessary replacement requirement for name changes, using the existing supported update method.

Custom roles already have create/read/update/delete and import in `openai_project_role`; the group membership and project group-role assignment resources already manage and import their corresponding grants. Update a custom role's permission set in place. Removing an assignment revokes that grant, but grants through other groups or organisation roles remain. These resources are additive: they detect drift in managed objects, not every extra assignment made elsewhere. Audit the service account's organisation/project assignments and group memberships before declaring its effective access least privilege. The new account's built-in role is checked on every refresh; unexpected `member` or `owner` on a role-free account blocks the run for reconciliation rather than silently retaining broader access or deleting the identity.

OpenAI combines all applicable roles by union. API-key scopes and [WIF mapping permissions](https://developers.openai.com/api/docs/guides/workload-identity-federation#configure-service-account-mappings) can only narrow that access. Therefore: account role union, intersected with the credential's restrictions, plus project model/hosted-tool policies. Mapping permissions do not alter long-lived keys or the identity's roles. Allow up to 30 minutes for [RBAC propagation](https://developers.openai.com/api/docs/guides/rbac), then test as the runtime identity, not the admin.

## Reusable example and migration

[The example](../../examples/runtime-identity/main.tf) takes environment, existing project ID, workload and an allowlisted inference permission set. Use independent inputs/state for Joy, pre-prod and prod. Multiple feature accounts can share a reviewed per-environment role/group; do not create organisation-wide grants. The five resources are the account, role, group, membership and project assignment. No key is generated or output. WIF targets the account ID; enable runtime rollout only after its role assignment has propagated and positive/negative checks pass.

Omitting `create_service_account_only` preserves legacy behaviour and state, including the initial sensitive key. Imports use `<project_id>/<service_account_id>`. Import infers role-free mode from `role=none`; it does not recover secrets or import role/group assignments. An imported member/owner with `create_service_account_only=true` is rejected at plan time until its built-in grants are removed; it is not replaced. The [vendor migration guidance](https://developers.openai.com/api/docs/guides/terraform/projects-and-access#remove-assignments) documents importing an existing default assignment and removing it through its assignment resource. Use this fork's `openai_project_user_role_assignment` with the actual project/account/role IDs; review and remove the built-in assignment, grant the approved custom role, refresh the account and require `role=none` before enabling the guard. Validate that path on a disposable legacy service account first; no live migration was tested. Existing keys are retained, so restrict/revoke them separately if required. Changing this policy on an already role-free account, a custom role's permissions, or an account name does not require replacement. Review migration separately and preserve `prevent_destroy` where appropriate; this PR does not modify platform accounts, pins or state. Prefer the assignment migration retaining the account ID, WIF target and application references. The provider does not automatically revoke grants/keys or replace accounts to enforce this policy.

For platform consumption, publish a **new immutable version** of this fork into `orbital.scalr.io/providers/openai`, then change the current `= 0.8.0-rc` pin to that exact published version and regenerate the lockfile. Do not overwrite the existing RC. Use the example shape in a separate reviewed platform migration; Anthropic currently pins `= 0.4.0-rc` and does not need this OpenAI change.

## Application operation audit

Inspected `orbitalwitness/project100x` at `7ca6e20ae8e1821f11793add7a6b40272192d10e`:

| Path / operation | Permission implications |
| --- | --- |
| `providers/router.py`, `openai_images.py` | PydanticAI Responses model, streaming and inline image preparation. `api.responses.write`; no direct OpenAI Files/Uploads call found in this runtime path. |
| `providers/decisions.py`, `model_tasks.yaml`, `providers/failover.py` | Explicit `openai-decisions:` utility tasks call Decisions without cross-provider failover. They require `api.decisions.write` ([vendor scope documentation](https://developers.openai.com/api/docs/guides/decisions-voice)). |
| `providers/failover.py` | Also constructs `OpenAIChatModel` for `openai:` fallbacks. Include `api.model.request` when that fallback is enabled. Failover must retain the destination vendor's restricted identity. |
| `agent/core.py`, `agent/tools/web_search.py` | Native web search is OpenAI-hosted inside a model request and subject to project hosted-tool policy. A function tool runs in the application; returning its result to the model is another inference request, not permission to manage provider accounts. The web-search function can itself make a separate model request. |
| `clients/openai_transcription.py` | Multipart audio POST to `/audio/transcriptions`, using a separately configured static key. Requires model capability access for that identity. It is not a `/files` upload; this PR does not migrate that credential. |
| `services/provider_image_files.py`, `providers/anthropic_cached.py` | Anthropic Files upload/delete helpers exist, but the current router disables the image-file resolver and keeps images inline, including normal failover. Inference-only works for that selected path; explicitly enabling Files later needs developer scope. |

This is a source audit, not a live SDK wire trace. Test streaming, native search, application tools, image inputs and both failover directions before rollout. Stored-response retrieval/deletion would require Responses read/write as applicable; OpenAI Files, Uploads, hosted file-search/vector stores, containers or batch operations need separately reviewed resource permissions and are not granted by this example. No OpenAI-hosted code execution is assumed from application-side shell/tool execution.

## API-key restrictions and verification limits

The Admin API supports [issuing a scoped service-account key](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/service_accounts/subresources/api_keys/methods/create) and [updating project-key scopes/expiry](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/api_keys/methods/update). These restrict a key, not its owner's roles or other credentials. Empty scopes means unrestricted within the owner's access, **not deny all**. The current documented project-key retrieval/response schema does not expose the scopes for reliable readback; this PR therefore does not invent a drift-detecting key-permissions resource. Generate keys outside Terraform only if needed, using a secret-manager workflow. Key creation returns the secret only once. WIF needs no key.

Local mock-backed tests verify Terraform lifecycle and HTTP contracts, not vendor authorisation. No OpenAI test admin credentials were available for a live test. Before rollout, use an authorised disposable project to verify role-free creation, assignment propagation, successful streaming/tool/image inference, denial of account/billing/organisation management, and WIF restrictions narrower than the account role. Check extra role/group grants separately. Keep provider CRUD permissions out of the runtime role.
