# Decisions

## 2026-07-29 — Bubble Tea line: v2 (charm.land)

Pinned at TUI kickoff, per plan: v1.3.x unless v2 is GA that week — and the
entire v2 line is GA (bubbletea v2.0.8, bubbles v2.1.1, lipgloss v2.0.5,
glamour v2.0.1, teatest/v2 tracks charm.land/bubbletea/v2). We take v2
everywhere under the `charm.land` module paths. **Never straddle**: no v1
charm dependency may enter the graph, and any future major bump migrates the
whole set in one change.

## 2026-07-29 — Experimenting is a beta beside the canonical, not a conflict

A developer trying something out used to have one option: leave the document
dirty. Sync then skipped it, so the team's updates never landed, the merge
base rotted, and the row sat in `CONFLICTED` — a state that means *resolve me
now*, which an experiment is not.

A local variant splits the two roles the file was serving. The canonical
document keeps syncing, always clean, always current. `<name>.beta.md` is the
developer's, and it is what installs into projects — **under the canonical
name**, so a harness still sees exactly one skill by that name. Two skills
with overlapping purpose in one context window is how you get an agent that
follows neither, which is why a renamed side-by-side variant is deliberately
*not* what this does; `fulcrum skills new` covers that case.

The variant carries its own merge base (the canonical it forked from), so
`fulcrum merge` pulls the team's newer version into it and publishing sends a
truthful `base_digest` however far the canonical has moved. `status` exits 1
only when the canonical has moved past the variant — running your own version
is a choice, not staleness; the team moving is the thing worth acting on.

## 2026-07-29 — The workspace is not a git repo

Sync state is three content snapshots plus SHA-256 digests: the pristine copy
from the last sync (`.fulcrum/base/`), the working file, and the server's
digest recorded at that sync. Classification compares hashes; diffs and
merges run over the snapshots. That is the same *shape* as a git merge base,
without a git object database.

We keep it that way. The workspace is shared with the feature-frozen Ruby
client, which knows nothing about git; the server is already the versioned
store (skill versions carry their own provenance); and making the directory a
repo would mean inventing synthetic branches for "what the server has" —
mapping fulcrum's model onto git's rather than the reverse. What git actually
buys — a real three-way merge — we implement directly in `internal/diffx`,
verified to produce byte-identical output to `git merge-file` on the same
inputs.

External merge tools stay reachable through the standard interchange format:
a conflicted merge writes git-style conflict markers, and `e` hands the file
to `$EDITOR`, so lazygit, nvim's diff mode, or anything else works without
fulcrum knowing about them. If a workspace git history is ever wanted for
browsing, that is an additive follow-up (init a repo, commit on sync) and does
not change the model above.

## 2026-07-29 — TUI v1 diff descope

Diffs render as unified *text* diffs (go-udiff, lipgloss-colored) for JSON
and markdown alike. The structural JSON-path diff and the three-way
conflicted panel are v1.1: risk H1 in the plan pre-commits this descope so
the diff screen cannot hold the v0.1.0 tag.

## 2026-10-09 — OMP telemetry reads the transcript directly; the hook lives at user scope

`fulcrum hook stop` now parses OMP session files as well as Claude Code's,
detected per record rather than by a flag: OMP writes `type: "message"` with
the speaker in `message.role` (`user` / `assistant` / `toolResult`) and usage
as `{input, output, cacheRead, cacheWrite}`, one record per model response
keyed by the record id. The turn arithmetic — count a response once, never
let tool output end a turn — is unchanged, so both harnesses produce the
same numbers for the same work. We did not add an OMP-specific payload: the
OMP hook factory builds the Claude-shaped Stop payload itself from
`ctx.sessionManager.getSessionFile()` / `getSessionId()`, so the command has
one input contract.

The factory is written to `~/.omp/agent/hooks/post/fulcrum-telemetry.ts`,
not the project's `.omp/hooks/`. Same reasoning as the Claude hook staying
out of the committed settings file: it names this machine's binary by
absolute path, and a hook is a side effect a teammate did not ask for. OMP
loads user hooks in every project, which is harmless — with no pin the
command records nothing. The MCP server entry for OMP is the existing
`.mcp.json`, which OMP reads as its portable fallback; writing
`.omp/mcp.json` too would register the server twice.

Two events because OMP fires them differently per session kind:
`session_stop` for the main session (awaited before settle, so the
transcript is complete; never fires for subagents) and `agent_end` for
subagent sessions, whose transcripts sit inside the parent's session
directory under the agent's name. A subagent's whole assignment is one turn,
which is what it is: one prompt, one hand-back.

## 2026-10-09 — Telemetry follows the session, with the checkout pin as fallback

The checkout pin is one card per checkout: right for a developer at a
keyboard, wrong the moment a session fans work out to subagents that each
pick up a card, because they all share the checkout. So `start_work` now
pins the SESSION: when it passes through the MCP bridge carrying a
`session_ref`, the bridge fetches the card's brief (as `fulcrum work` does)
and writes `<config dir>/session-pins/<ref>.json` with the numeric ids and
role; `fulcrum hook stop` reads that pin for the transcript's session id
before falling back to the checkout's. One file per session because several
bridges and hooks run at once; written whole and renamed so a hook never
reads a torn pin.

The model does not know its own session id, and should not have to. The OMP
hook factory fills `session_ref` into `start_work` / `finish_work` calls
from `ctx.sessionManager.getSessionId()` when the model left it blank, and
keeps one the model named. The pin is never cleared by `finish_work` — the
turn that called it is still being written when the hook fires — and is
replaced by the session's next `start_work`.

Claude Code gets the same two halves through its own hooks. `SubagentStop`
carries the parent's session_id plus the subagent's agent_id and
agent_transcript_path — verified against real payloads on 2026-10-09 — so
`hook stop` reads the subagent's file and records it under
"<session_id>/<agent_id>", the ref `SessionRef` builds; a subagent's
PreToolUse payload carries the same agent_id, so `hook tool-use` names the
call identically and the pin and the transcript meet. The PreToolUse
answer uses Claude Code's `updatedInput` contract with `permissionDecision:
"allow"`, which is honest for the two tools it is narrowed to: they only
open and close a work episode. Codex and Kimi remain declined for
telemetry; nothing here changes what they record, which is nothing.
