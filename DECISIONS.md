# Decisions

## 2026-07-29 — Bubble Tea line: v2 (charm.land)

Pinned at TUI kickoff, per plan: v1.3.x unless v2 is GA that week — and the
entire v2 line is GA (bubbletea v2.0.8, bubbles v2.1.1, lipgloss v2.0.5,
glamour v2.0.1, teatest/v2 tracks charm.land/bubbletea/v2). We take v2
everywhere under the `charm.land` module paths. **Never straddle**: no v1
charm dependency may enter the graph, and any future major bump migrates the
whole set in one change.

## 2026-07-29 — TUI v1 diff descope

Diffs render as unified *text* diffs (go-udiff, lipgloss-colored) for JSON
and markdown alike. The structural JSON-path diff and the three-way
conflicted panel are v1.1: risk H1 in the plan pre-commits this descope so
the diff screen cannot hold the v0.1.0 tag.
