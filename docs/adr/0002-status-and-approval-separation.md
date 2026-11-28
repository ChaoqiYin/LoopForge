# 0002. Separate external business status, internal execution status and business approval

- Status: Accepted
- Date: 2026-11-26
- Deciders: LoopForge platform
- Related: #1, #4, #5, #9

## Context

The old model mixed the external status of a requirement with the local status
of its execution. It also implied that LoopForge owned approvals: the draft
document proposed an `Approval` entity and approval requests issued by the
platform.

External management owns business state and approvals. LoopForge owns
execution evidence. Sharing one field or one state machine makes external
edits overwrite local execution, makes a successful Agent node look like a
completed business requirement, and creates a second approval authority.

## Decision

Keep three concepts separate.

1. **External Business Status**
   Stored read-only in `ExternalRef.ExternalState`. It is maintained by the
   external platform and never written to `Issue.Status`.
2. **Internal Execution Status**
   Derived on read from `Issue.Status` plus the latest Task/Run state. There
   is no `execution_status` column and no second state machine.
   - `Issue.Status`: `backlog`, `todo`, `in_progress`, `in_review`, `done`,
     `closed`, `blocked`, `cancelled`.
   - Run status: `queued`, `running`, `waiting`, `succeeded`, `failed`,
     `cancelled`.
   - There is no paused Run status. Pause is `PauseRequested` with the Run
     state kept at `waiting`.
   - `waiting_approval` is a node state for an Execution Gate, not a Run or
     Work Item status.
3. **Business Approval**
   Owned by the external platform. LoopForge does not define an approval
   entity, does not create an `approvals` table, does not check approval
   permissions and does not issue approval requests. Approval results arrive
   as observed external business status values.
4. **Execution Gate**
   A SOP-level human checkpoint that controls execution only. It is not a
   Business Approval and cannot approve on behalf of the business.

Status mapping keys `(connection, table, external_status)` to one action:
`start_sop`, `ignore`, `pause`, `cancel`, `restart` or `writeback`. Actions
fire only on a status change. Unknown statuses are synced and observed
without triggering.

## Consequences

- The execution center shows two columns: external business status and
  derived internal execution status.
- An Agent node succeeding never completes the external business status.
- The approval center is read-only: it lists external pending approvals and
  internal gates, with no approve or reject action.
- Status regressions are recorded and alerted, never rolled back into code.
- Only one active Run is allowed per Work Item; actions received while a Run
  is active are kept as a single pending action.

## Alternatives considered

- **Map external status onto `Issue.Status`:** simple, but destroys local
  execution state and makes writeback loops likely.
- **Persist a derived execution status:** avoids recomputation, but creates a
  second state machine that drifts from Run data.
- **Build an Approval entity and API:** gives LoopForge an approval authority
  that the external platform already owns.
