---
name: estimating-with-fulcrum
description: >-
  Estimate a unit of work against THIS project's Fulcrum context — its rubric, delivery roles,
  complexity scale, and the features the team has already priced — grounded in evidence gathered
  from the actual repository. Use when asked how long something would take, what an approach
  would cost, whether a refactor or dependency swap is worth it, or to size an idea before
  taking it to Fulcrum. Produces a features-json block that can be pushed back to the project.
  Do not use for questions with no effort dimension, or to estimate a codebase this checkout is
  not part of.
---

# Estimating with Fulcrum

Answer "how long would this take?" the way Fulcrum would, with one advantage Fulcrum does not
have: you can read the code.

Fulcrum's entire knowledge of this repository is a language histogram and a list of dependency
names. You are sitting in the checkout. That asymmetry is the point of this skill — the method,
vocabulary, and contract are identical to the web app's, and the evidence is better.

## Before anything else

The project context must be present at `.fulcrum/project-context.md`.

```
fulcrum context --project <id-or-name>
```

Run it at the start of an estimating session so the rubric, roles, scale, releases, and priced
feature inventory are current. If it fails (offline, no token), you may proceed against what is
already on disk — but **say so in your answer and give the file's age**. A silently stale
estimate is the failure mode to design against.

Read `.fulcrum/project-context.md` before estimating. It is the authority on method and
vocabulary; nothing in this file overrides it.

## Step 1 — Survey the repository

Gather evidence before reasoning about effort. Be targeted: you are building a specific factual
picture, not reading the codebase.

For a dependency swap or refactor:

- Every import and call site of the outgoing thing — **count them**
- How they are used, grouped by kind. A plain list is not the same work as one with custom
  renderers, sorting, server-side pagination, or virtualization
- Which of those files have tests, and which do not
- Whether the incoming thing covers each capability actually in use — check its docs when the
  answer is not obvious, rather than assuming parity

For a new feature:

- Where it would live, and what already exists that resembles it
- What it would touch — models, endpoints, views, jobs, migrations
- What existing patterns it can follow, and where it would be inventing something

Write the result to `.fulcrum/survey.json`, then cite it. Counts belong in your reasoning: "23
call sites, 4 with custom renderers, none covered by tests" is what justifies a range. A number
with no evidence behind it is exactly the internet-prior guess this skill exists to replace.

## Step 2 — Map the survey onto the rubric

Walk the survey against the rubric's components and the organization's delivery roles, both of
which are in the bundle.

**Consider every component.** For each, either include its share or state why it does not apply.
This is the rubric's coverage requirement: silence must never be read as absence of work. A
drag-and-drop change has real accessibility surface; a backend-only change does not — but say so
either way rather than leaving it unmentioned.

Split by role. Fulcrum's unit is one estimate per feature **per delivery role**, and roles marked
support or advisory never receive feature estimates. Use the exact role names from the bundle.

Anchor against the priced feature inventory. It shows how this team has actually sized
comparable work. Land near comparable numbers, or say in your rationale why this is different.
This is the single strongest correction to a model's prior about how long things take.

## Step 3 — Emit and compute

Produce a `features-json` block in the format the bundle's output contract specifies: `action`,
then `features[]` with `id`, `name`, `description`, `prd`, `moscow_priority`, optional `release`,
and `estimates[]` of `{role, estimate}`.

Each estimate carries a three-point range — `low`, `likely`, `high` in hours — plus `confidence`,
`components_included`, `components_excluded` with reasons, `assumptions`, `exclusions`,
`unknowns`, `dependencies`, `risks`, `reuse_or_modification`, and `rationale`.

Reason in three values. A long tail belongs in `high`, not absorbed into `likely`. A degenerate
range where all three are equal is legitimate for well-understood work — never invent a spread to
look thorough.

Then hand it to the CLI:

```
fulcrum estimate .fulcrum/draft.json
```

**Never compute the committed hours yourself.** It is derived as `(low + 4·likely + high) / 6`
and snapped to this project's complexity scale, and the CLI does that with the same rule the
server uses, pinned by fixtures. Arithmetic done by a language model is the weakest link in an
otherwise reproducible chain. Emit the range; let the tool commit the number.

If the CLI reports contract violations, fix the draft and re-run. It refuses rather than
repairing, exactly as the server does.

## Hard rules

- **Never emit an `hours` field.** The committed value is derived, never supplied — that is what
  stops an estimate committing to a number its own range does not support.
- **Never claim a dependency you have not seen** in the bundle's feature inventory. Inventing one
  is a contract violation.
- **Never name a `release` that is not in the bundle's release list.**
- **Never estimate for a support or advisory role.** They are staffed across the engagement.
- **Do not pass off a stale bundle as current.** Say its age when you could not refresh.
- Descriptions and PRDs are HTML, not Markdown — `<p>`, `<ul><li>`, `<strong>`. Markdown syntax
  renders as literal text in Fulcrum's editors.

## Pushing it back

A local estimate is a scratchpad by default. When it is worth keeping:

```
fulcrum feature push .fulcrum/draft.json --project <id-or-name>
```

**Append only.** The server refuses every action but `add`, so a push adds to the backlog and can
never modify or remove work already in the project; an id that already exists is skipped rather
than overwritten. The full payload is shown before anything is sent, and declining is safe.

Committed hours are derived server-side from your ranges, through the same applier the chat uses.
An estimate that fails the organization's rubric contract is dropped and named — never repaired
into something that merely looks valid.

Do not push on your own initiative. It puts work in front of a team; ask first.
