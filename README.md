# fulcrum

The subscriber CLI for [Fulcrum](https://usefulcrum.ai): sync your
organization's codified estimation knowledge to your machine, see what
drifted, and publish your edits back as reviewable proposals. Bare
`fulcrum` opens an interactive terminal UI; every subcommand stays
scriptable for CI and agent harnesses.

**Status: pre-release.** The API kernel and state engine are done and
golden-tested against the server's vendored contract corpus; the CLI verbs
and TUI are landing next. Nothing is tagged yet.

## Quickstart

1. Sign in to your Fulcrum server and mint a personal API token at
   **Settings → Developer** (`/settings/developer`). It is shown exactly once.
2. Install:

   ```sh
   brew install headwayio/fulcrum/fulcrum
   # or
   curl -fsSL https://usefulcrum.ai/install.sh | sh
   ```

3. Connect and sync:

   ```sh
   fulcrum login    # server URL + token, validated live, stored in your OS keychain
   fulcrum sync     # documents land in ~/.fulcrum/skills
   ```

When a document moved on both sides, `fulcrum merge` (or `m` in the TUI)
three-way merges the server's version into yours against the copy from your
last sync — clean where the changes do not overlap, git-style conflict
markers where they do. If your side is not worth keeping, `fulcrum revert`
(or `x`) takes the server's version instead and files your copy under
`.fulcrum/discarded/`.

Trying something out? `fulcrum skills beta <slug>` (or `b` in the TUI) makes
your version `<name>.beta.md` and lets the canonical one keep syncing beside
it. Your beta is what installs into projects — under the canonical name, so
agents still see one skill by that name — `fulcrum merge` pulls the team's
newer version into yours, publishing proposes yours as the team's next, and
`--drop` hands authority back.

`fulcrum skills install <project-dir>` writes the org's skills into every
harness format a project might read, because one project often drives
several agents:

| format   | path                              | read by                                  |
| -------- | --------------------------------- | ---------------------------------------- |
| `claude` | `.claude/skills/<slug>/SKILL.md`  | Claude Code                              |
| `shared` | `.skills/<slug>/SKILL.md`         | Kimi Code, OpenCode, other SKILL.md agents |
| `agents` | managed block in `AGENTS.md`      | Codex, Kimi Code, the AGENTS.md convention |

The project is remembered, so every later `fulcrum sync` refreshes all of
them — no harness is left reading yesterday's skills. Only fulcrum's own
block in `AGENTS.md` is touched; the rest of that file is yours. Narrow it
with `--target claude|shared|agents|auto` if you want less.

Then run `fulcrum` with no arguments for the interactive view, or script it:
`fulcrum status --json` exits 0 when everything is fresh, 1 when anything is
behind/drifted/conflicted, 2 on network/auth/config errors.

Scripted setups skip `login` entirely — the Ruby reference client's exact
four env vars work here too: `FULCRUM_URL`, `FULCRUM_API_TOKEN`,
`FULCRUM_ORG_ID`, `FULCRUM_SKILLS_DIR`.

## Design commitments

- **Nothing leaves your machine without an explicit confirmation.** Publishing
  is per-document and opt-in; `push-facts` shows the full payload first.
- **The sync workspace is shared.** `.fulcrum-sync.json` is byte-compatible
  with the Ruby reference client (`bin/fulcrum-skills` in the server repo);
  either client can read the other's state.
- **The contract is pinned.** `corpus/` vendors the server's golden fixtures;
  CI replays them byte-exact, so cross-repo drift fails a build instead of a
  user.

## Development

```sh
go test ./...
go vet ./...
```
