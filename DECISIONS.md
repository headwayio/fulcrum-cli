# Decisions

## 2026-07-29 — Bubble Tea line: v2 (charm.land)

Pinned at TUI kickoff, per plan: v1.3.x unless v2 is GA that week — and the
entire v2 line is GA (bubbletea v2.0.8, bubbles v2.1.1, lipgloss v2.0.5,
glamour v2.0.1, teatest/v2 tracks charm.land/bubbletea/v2). We take v2
everywhere under the `charm.land` module paths. **Never straddle**: no v1
charm dependency may enter the graph, and any future major bump migrates the
whole set in one change.

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
