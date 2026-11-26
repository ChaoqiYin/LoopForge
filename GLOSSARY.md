# LoopForge

LoopForge executes work that is managed elsewhere. This glossary fixes the vocabulary for the boundary between external work management and internal execution.

## Language

**Work Item**:
A unit of work owned by an external management platform, represented inside LoopForge as a projection plus execution context. It is never an independent source of truth.
_Avoid_: requirement, issue, task (as umbrella terms)

**Requirement**:
A Work Item whose kind is `requirement`; it expresses a business need.
_Avoid_: demand, story

**Issue**:
A Work Item whose kind is `issue`; it expresses an executable development task.
_Avoid_: ticket, task

**External Record**:
The authoritative record in a management platform that a Work Item projects.
_Avoid_: source issue, remote ticket

**Business Key**:
The stable identifier a table stores for one External Record. A Work Item's identity is `(connection, table, business_key)`, so a copied or recreated record still resolves to the same Work Item.
_Avoid_: record id, external id (the platform row reference is not stable across copies)

**Event Inbox**:
The durable intake table for platform events. Each event is persisted under its unique `event_id` before the platform is acknowledged, then processed asynchronously, in order per record, and idempotently.
_Avoid_: webhook queue, event log

**External Business Status**:
The status value maintained by the external platform. LoopForge reads it and never treats it as its own execution state.
_Avoid_: status, issue status

**Internal Execution Status**:
The status derived from LoopForge's dispatch, task, and run state. It is never written back as a business status.
_Avoid_: run status, task status

**Management Connection**:
A configured link to one external management platform instance: its encrypted application-credential reference, base and table, field mapping, status mapping, default Project, writeback switch and sync cursor.
_Avoid_: integration, tracker

**Reconciliation**:
A re-read of External Records used as the fallback for events: incremental every 15 minutes, full daily, and on demand. It recovers lost events and deletions, and refreshes values that do not raise events.
_Avoid_: sync, polling

**Management Adapter**:
The boundary that translates between one management platform's records and Work Items.
_Avoid_: tracker, sync

**Tracker**:
The legacy external-sync service that predates Management Adapter. It is not the new boundary.
_Avoid_: using it for Management Adapter

**Project**:
The execution configuration container a Work Item belongs to: default SOP, repositories, and members. It does not own requirements.
_Avoid_: workspace, requirement space

**Delivery**:
A record of one development delivery produced by LoopForge: repository, branch, commit, test and review results, and final state.
_Avoid_: MR, PR (those are provider-specific details)

**Business Approval**:
A human decision owned by the external management platform. LoopForge observes the result but does not define or enforce it.
_Avoid_: gate, approval (as umbrella)

**Execution Gate**:
A human checkpoint inside a SOP run that controls execution. It is not a Business Approval.
_Avoid_: approval, business approval

**Formal Work Item**:
A Work Item bound to an External Record. Only Formal Work Items enter delivery statistics and the default execution view.
_Avoid_: official task, real issue

**Orphaned Work Item**:
A Work Item whose External Record was deleted in the management platform. It stops automatic triggering but keeps its Run and Trace evidence.
_Avoid_: deleted issue, zombie work item

**Temporary Run**:
A run started for exploration or assistance without a Work Item. It is excluded from formal execution statistics.
_Avoid_: scratch task, ad-hoc issue
