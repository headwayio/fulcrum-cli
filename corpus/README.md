# API contract corpus

Byte-exact goldens for Fulcrum's `/api/agent_context` contract (contract 1),
vendored from the Fulcrum server repo. **Never edit by hand.**

They are generated there by `bin/rails api_contract:regenerate` under a frozen
clock against a canonical seeded organization, and a server-side spec fails
whenever live API output diverges from these bytes — so drift breaks the
server build first. `corpus.sha256` is the integrity listing; CI here verifies
it, and `internal/api`'s tests decode every fixture.

`state/fulcrum-sync.json` was produced by the Ruby reference client
(`bin/fulcrum-skills`) syncing these exact documents: it is the state-file
compatibility contract both clients honor when sharing a workspace dir.

To update: regenerate in the server repo, copy `spec/fixtures/api_contract/`
over this directory, and commit both sides.
