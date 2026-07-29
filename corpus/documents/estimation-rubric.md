---
name: estimation-rubric
organization: Corpus Primary Organization
rubric_id: organization-default-estimation
rubric_digest: a67ac50d111b51163f4b7cf900eaa1f329bf2620de53cb2a1c5eefc438c2125c
version: 1
generated_at: 2026-07-01T07:00:00-05:00
source: fulcrum
---

# Estimation rubric — Corpus Primary Organization

Rendered from the organization's active rubric. This file is generated:
edit `estimation-rubric.json` and publish it as a proposal instead of
editing this rendering.

## Estimation Rubric

Estimate each feature per role against this rubric. Estimates are one per feature per delivery role (level: feature-role).

Reason in three values — low, likely, high — plus a confidence level. A long tail belongs in high rather than being absorbed into likely, and low == likely == high is legitimate for well-understood work. Emit every estimate as a structured object (the feature output format below shows the shape): low, likely and high in hours, confidence, components_included, components_excluded with reasons, assumptions, exclusions, unknowns, dependencies, risks, reuse_or_modification, and rationale. The committed value is the expected value, computed as (low + 4 * likely + high) / 6. It is never supplied directly: the system derives it from your range and snaps it to the nearest step of this project's complexity scale.

Components marked delivery-component are sized per feature and per role — only delivery roles receive these estimates. Components marked support-coverage are staffed across the engagement and are never added to per-feature estimates.

Consider every component. For each, either include its share in the estimate or note in your reasoning why it does not apply — never leave one silently unconsidered.

- `discovery` (delivery-component) — Discovery; applies when: The brief leaves a question open whose answer would change the shape of the work, not merely its size. Includes: Reading the existing implementation well enough to know what is already there; Spiking a risky approach far enough to tell whether it works; Getting a decision from whoever owns an unresolved product question. Excludes: Routine familiarisation with a codebase the team already works in; Design exploration of a settled requirement, which is ux_design; Writing the code the spike proved out, which lands in its own component.
- `ux_design` (delivery-component) — UX and visual design; applies when: The feature introduces or changes something a person looks at or operates. Includes: Interaction and state design, including empty, loading, error and permission-denied states; Visual design and its fit with the existing design system; Design review and the revisions it produces. Excludes: Implementing the design in markup and styles, which is frontend; Deciding whether the feature should exist, which is discovery; Accessibility conformance work, which is accessibility.
- `frontend` (delivery-component) — Frontend; applies when: The feature has a user-facing surface. Includes: Markup, styling and client-side behaviour; Client-side validation and the error states it surfaces; Wiring to the server contract, including loading and failure handling. Excludes: The server endpoints being called, which is backend; Designing what is being built, which is ux_design; Automated coverage of the result, which is testing.
- `backend` (delivery-component) — Backend; applies always. Includes: Domain logic and the data models the behaviour lives on; Request handling, routing, serialization and authorization rules; Background jobs and scheduled work the feature requires. Excludes: Schema changes and data backfills, which are data_migrations; Calls out to third-party systems, which are integrations; Automated coverage of the result, which is testing.
- `data_migrations` (delivery-component) — Data and migrations; applies when: The feature adds, reshapes or backfills persisted data. Includes: Schema changes, indexes and constraints; Backfills, and the rehearsal of one against production-shaped volumes; The ordering that keeps a deploy safe while old and new code overlap. Excludes: The application code reading the new shape, which is backend; Sequencing the release itself, which is deployment; Reversibility for users after release, which is rollout.
- `integrations` (delivery-component) — Third-party integrations; applies when: The feature depends on a system the team does not control. Includes: Client code, authentication and credential handling; Retry, timeout and partial-failure behaviour; Sandbox setup and whatever the provider's review or approval requires. Excludes: Internal services the team owns, which are backend; Storing what comes back, which is data_migrations; Alerting on the integration failing, which is observability.
- `testing` (support-coverage) — Automated testing; applies always. Includes: Unit and integration coverage of the behaviour being added; End-to-end coverage where the value is in the browser rather than the response; Characterising existing behaviour before changing it. Excludes: Manual verification by the person who wrote it, which is inside each delivery component; Client acceptance testing, which is project_management; Load and performance testing, which is estimated explicitly when required.
- `accessibility` (support-coverage) — Accessibility; applies when: The feature has a user-facing surface. Includes: Keyboard operability and visible focus; Semantics and labelling that assistive technology depends on; Colour contrast, motion preferences and reading order. Excludes: Visual design choices themselves, which are ux_design; A formal external audit, which is estimated explicitly when required.
- `security_privacy` (support-coverage) — Security and privacy; applies when: The feature touches credentials, personal data, payment details, or changes an authorization boundary. Includes: Authorization rules and the tests that pin them; Handling of secrets and personal data, including what is logged; Rate limiting and abuse paths on anything unauthenticated. Excludes: Routine authentication already provided by the application; A formal penetration test, which is estimated explicitly when required.
- `observability` (support-coverage) — Observability; applies when: The feature introduces a failure mode nobody would otherwise notice. Includes: Logging and error reporting at the boundaries that can fail; Metrics or alerts for the failure that matters; Whatever an on-call person needs to tell healthy from broken. Excludes: Product analytics, which is a delivery component of its own; Standing infrastructure monitoring already in place.
- `deployment` (delivery-component) — Deployment; applies when: Releasing the feature needs more than the standard pipeline: new infrastructure, new configuration, or an ordered sequence. Includes: New services, environment variables, secrets or scheduled jobs; Build and pipeline changes the feature requires; The deploy ordering that keeps the system working mid-release. Excludes: An ordinary deploy through the existing pipeline; The migrations themselves, which are data_migrations; Exposing the feature to users, which is rollout.
- `rollout` (delivery-component) — Rollout; applies when: The feature changes behaviour for people or data that already exist. Includes: Feature flags, staged exposure and the removal of both afterwards; Communicating the change to the people it affects; The path back if it goes badly. Excludes: Getting the code onto the servers, which is deployment; Reshaping the data, which is data_migrations.
- `project_management` (support-coverage) — Project management; applies always. Includes: Planning, estimation and the ceremonies the engagement runs; Client communication, demos and acceptance; Coordination across roles and unblocking. Excludes: Technical decisions made while building, which sit in their own components; Resolving open product questions, which is discovery.

Confidence levels:

- `low` — Low: A material unknown remains, and resolving it could change the approach rather than only the size. The range is wide because it is honest, not because the estimator was careless. Requires discovery before this estimate is committed.
- `medium` — Medium: The approach is settled and the work is understood, but it has not been done in this codebase before, so the range carries genuine spread.
- `high` — High: Work of this shape has been done here before and the pattern to follow is visible in the codebase. The range is narrow because the uncertainty really is small.

Confidence is your proposal; a human reviewer confirms or overrides it, and both values are kept.

No calibration multipliers are in force. Do not apply a fudge factor of your own: carry uncertainty in the range and the confidence level instead.
