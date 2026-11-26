# 0001. Reuse `issues` as the Work Item projection

- Status: Accepted
- Date: 2026-11-26
- Deciders: LoopForge platform
- Related: #1, #9

## Context

LoopForge already stores requirements and development tasks in the `issues`
table, and dispatch, scheduling, clarification, delivery and the UI all read
that model. The external work management change needs a local object that
represents one External Record and carries execution context.

The obvious alternatives were a new `work_items` table, or keeping separate
`Requirement`, `Issue` and `Approval` entities. Both would create a second
management model, force a second set of APIs and handlers, and leave existing
execution paths reading a different object.

## Decision

Use the existing `issues` table as the Work Item projection.

- `Issue.Kind` distinguishes `requirement` from `issue`. They are kinds of one
  Work Item, not parallel entities.
- `ExternalRef` stores the authoritative identity and snapshot:
  `business_key`, `record_id`, `external_url`, `external_state`, `orphaned`,
  status history and writeback metadata.
- `(connection_id, table_id, business_key)` is the stable identity. The
  `record_id` is only the platform's current row reference.
- Internal APIs and `/api/issues` remain the read surface; Work Item is the
  domain and UI vocabulary.
- No `work_items` table and no separate requirement API are introduced.

## Consequences

- Existing dispatch, Run, scheduling and Delivery code keeps working against
  one model instead of being rewritten behind an adapter.
- The table must not be treated as an authority. A Formal Work Item is derived
  from its External Record binding; local creation paths are closed.
- External business status cannot be written into `Issue.Status`; it lives in
  `ExternalRef.ExternalState` (ADR 0002).
- Payload JSON remains the extension point for fields that are not indexed.
  Only identity and query columns are physical.
- Legacy rows that were not external records are removed at the one-time
  cutover (ADR 0005).

## Alternatives considered

- **New `work_items` table:** cleaner naming, but duplicates the Issue model,
  breaks existing queries and requires a parallel API and UI migration.
- **Separate Requirement and Issue entities:** matches the pre-change document,
  but creates two objects for one external record and leaves approval as a
  third local authority.
