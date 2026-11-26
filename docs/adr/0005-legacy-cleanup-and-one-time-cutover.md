# 0005. Legacy cleanup and one-time cutover

- Status: Accepted
- Date: 2026-11-26
- Deciders: LoopForge platform
- Related: #1, #9

## Context

After the external work management change, LoopForge must not keep a second
authoritative requirement store. Existing `issues`, `issue_comments`,
`requirement_spaces` and chat clarification sessions contain locally managed
business data from the old model. Execution evidence in `tasks`, `runs`,
`run_events` and traces must survive because it is the audit trail of what
actually ran.

Deleting production data automatically at startup is unacceptable. Operators
need to preview counts, take a backup, choose a maintenance window and keep a
rollback option.

## Decision

Ship a manual cleanup migration, not startup logic.

- `migrations/043_external_work_cleanup_dry_run.sql` reports the pending row
  count per table and the preserved counts for execution evidence.
- `migrations/043_external_work_cleanup.sql` copies the rows it will remove
  into `*_cleanup_043_backup` tables, then deletes:
  - legacy `issues` (everything without `source = 'feishu_bitable'`),
  - comments whose Issue is not a preserved Work Item,
  - requirement spaces linked to a removed or missing Issue,
  - clarification sessions linked to a removed or missing Issue, plus
    unlinked sessions.
- The script never drops a business table. It never touches `tasks`, `runs`,
  `run_events` or trace evidence. Their `issue_id` may become dangling, which
  is expected.
- Work Items projected from the Management Connection
  (`source = 'feishu_bitable'`) are preserved, so rerunning the script after
  go-live is safe and idempotent.
- Operational order:
  1. Stop every database writer and take a full dump.
  2. Run the dry-run script and review the counts.
  3. Run `043_external_work_cleanup.sql`.
  4. Start the upgraded binary, configure the Management Connection and run
     the first full sync.
  5. Keep the dump and backup tables for the rollback window; drop the backup
     tables only after a verified sync and an accepted rollback decision.
- Rollback inside the window means restoring the dump and running the previous
  binary. Restoring only the backup tables does not recreate external state
  written after the cutover.

## Consequences

- The cutover is explicit and reviewable; no operator can trigger it by
  restarting the platform.
- Run and Trace history stays readable even though its Work Item row is gone.
  The UI must render history from the run snapshot when the Work Item is
  missing.
- The cleanup is repeatable, but removing backup tables ends the in-database
  rollback copy.
- Fresh installations do not run this migration.
- Verification is a MySQL test that applies both scripts in a disposable
  schema, checks the dry-run counts, checks preservation, and runs the script
  twice.

## Alternatives considered

- **Automatic cleanup in `repo.Open`:** violates the repository rule that code
  does not migrate or seed data.
- **Drop and recreate tables:** destroys execution evidence and makes rollback
  ambiguous.
- **Delete everything including execution evidence:** loses the audit trail
  the platform exists to produce.
