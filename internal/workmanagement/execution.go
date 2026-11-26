package workmanagement

import (
	"context"

	"agentflow/internal/domain"
)

// RunExecution is the execution side of a status action. The existing Issue
// dispatch chain implements it, so Work Management never starts Runs or
// controls them directly: it only translates an external status entry into a
// dispatch, pause, cancel or restart request.
//
// A nil RunExecution disables automatic actions. Projection, status snapshot
// and audit still work, which keeps read-only deployments and unit tests
// useful without an engine.
type RunExecution interface {
	// ActiveRun returns the Work Item's non-terminal Run, if any. queued,
	// running and waiting all count as active.
	ActiveRun(ctx context.Context, workItemID string) (RunRef, bool, error)
	// Dispatch starts the resolved SOP for the Work Item. WorkflowID is the
	// status mapping's SOP; when empty the dispatch chain resolves the Issue
	// binding, then the Playbook, then the project default.
	Dispatch(ctx context.Context, req WorkItemDispatch) (RunRef, error)
	// Pause requests the active Run to pause at a human decision point.
	Pause(ctx context.Context, runID string) error
	// Cancel cancels the active Run.
	Cancel(ctx context.Context, runID string) error
}

// RunRef identifies one Run for status action auditing.
type RunRef struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflowId,omitempty"`
	Status     string `json:"status,omitempty"`
}

// WorkItemDispatch is the status-action request handed to the execution side.
type WorkItemDispatch struct {
	WorkItemID     string
	WorkflowID     string
	ExternalStatus string
	// Rerun asks the dispatch chain to run the SOP again when it already has
	// a Task for this Work Item. The first dispatch still uses the primary
	// reservation; a restart is explicitly a rerun.
	Rerun bool
	// AllowRerun lets a status entry start the SOP even though an earlier
	// Task is already recorded. The dispatch chain reuses its rerun
	// reservation instead of answering "already ran".
	AllowRerun bool
	// TriggerOrigin is the audit origin recorded on the Task.
	TriggerOrigin string
}

// noopExecution is used when no execution side is configured.
type noopExecution struct{}

func (noopExecution) ActiveRun(context.Context, string) (RunRef, bool, error) {
	return RunRef{}, false, nil
}

func (noopExecution) Dispatch(context.Context, WorkItemDispatch) (RunRef, error) {
	return RunRef{}, nil
}

func (noopExecution) Pause(context.Context, string) error  { return nil }
func (noopExecution) Cancel(context.Context, string) error { return nil }

var _ RunExecution = noopExecution{}

// statusActionOperation names the audit actions emitted by status triggers.
const (
	operationActionApplied    = "external_action"
	operationActionPending    = "external_action_pending"
	operationActionSkipped    = "external_action_skipped"
	operationActionFailed     = "external_action_failed"
	operationStatusObserved   = "external_status_observed"
	operationStatusRollback   = "external_status_rollback"
	operationWriteback        = "external_writeback"
	operationWritebackIgnored = "external_writeback_observed"
)

// systemActor is the audit actor for every automated external transition.
// External sync never impersonates a platform user.
const systemActor = "system"

// statusTriggerOrigin is the Task trigger origin for external status actions.
const statusTriggerOrigin = "external_status"

// pendingAction returns a copy of the deferred action, if any.
func pendingAction(issue domain.Issue) (domain.WorkItemPendingAction, bool) {
	if issue.ExternalPendingAction == nil {
		return domain.WorkItemPendingAction{}, false
	}
	return *issue.ExternalPendingAction, true
}
