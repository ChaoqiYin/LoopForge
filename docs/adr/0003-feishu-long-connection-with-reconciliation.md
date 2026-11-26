# 0003. Feishu long-connection events with reconciliation as fallback

- Status: Accepted
- Date: 2026-11-26
- Deciders: LoopForge platform
- Related: #1, #3, #4, #9

## Context

LoopForge already runs a Feishu application over the official SDK's
long-connection dispatcher for chat Bots. The first management platform is a
Feishu Bitable base, and the deployment should not need a public callback URL.

Feishu event delivery has constraints that shape the design:

- Delivery is at least once, so duplicates occur.
- An event must be acknowledged within about three seconds.
- Record and field changes arrive per subscribed file (`app_token`).
- Formula and lookup fields do neither raise events nor appear in the event
  body.
- Date filters only reach whole days.
- `record_id` is not guaranteed stable across copies or recreation.
- A deleted record can only be learned from a delete event or a full re-read.

## Decision

Use long-connection events as the real-time path and periodic reconciliation
as the fallback.

- Subscribe the connection's base to
  `drive.file.bitable_record_changed_v1` and
  `drive.file.bitable_field_changed_v1`.
- Persist each event in the Event Inbox under its unique `event_id` before
  acknowledging it within the three-second window. The HTTP/event handler does
  no dispatch work synchronously.
- Process inbox rows asynchronously, idempotently, and serially per
  `record_id`. At-least-once delivery must not create duplicate actions.
- Re-read the record after a change event instead of trusting partial event
  bodies, especially for formula and lookup fields.
- Reconcile incrementally every 15 minutes, fully every day, and on demand
  from the configuration page. Truncate incremental results locally by
  `last_modified_time` because the platform's date filter is day-granular.
- A `record_deleted` event or a full reconciliation marks the Work Item
  `orphaned`; automatic triggering stops, Run and Trace evidence stay.
- Identify the system's own writeback and suppress the resulting event so a
  writeback cannot loop.
- A read-only synced table is rejected with a readable error; a field rename
  or type change pauses automatic writeback and raises an alert.

## Consequences

- No public callback endpoint or inbound firewall rule is required.
- Event handling is eventually consistent; reconciliation bounds the recovery
  time and covers lost events and deletions.
- Duplicate or out-of-order events are expected, so identity and actions must
  be idempotent and keyed by the Business Key.
- The Event Inbox and the reconciliation worker need their own failure,
  retry and observability story.
- The connection check must verify that the application can manage the base;
  failure marks the connection not ready rather than silently degrading.

## Alternatives considered

- **Webhook callbacks only:** requires public ingress and still needs
  deduplication and recovery.
- **Polling only:** simpler, but slow and wasteful; the platform has no
  millisecond-level incremental API.
- **Trust event bodies:** breaks on formula/lookup fields and partial
  payloads.
