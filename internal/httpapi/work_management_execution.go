package httpapi

import (
	"context"
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/workmanagement"
)

// workManagementExecution adapts the existing Issue dispatch chain to the
// Work Management status-action boundary. It deliberately adds no second
// dispatch path: an external status entry becomes the same Task/Run the manual
// route creates.
type workManagementExecution struct {
	deps *deps
}

var _ workmanagement.RunExecution = (*workManagementExecution)(nil)

// ActiveRun reads the Work Item's latest Task and Run. queued, running and
// waiting are active; succeeded, failed and cancelled are terminal.
func (x *workManagementExecution) ActiveRun(_ context.Context, workItemID string) (workmanagement.RunRef, bool, error) {
	issue, ok := x.deps.repo.Issue(workItemID)
	if !ok || strings.TrimSpace(issue.LatestTaskID) == "" {
		return workmanagement.RunRef{}, false, nil
	}
	task, ok := x.deps.repo.Task(issue.LatestTaskID)
	if !ok || strings.TrimSpace(task.RunID) == "" {
		return workmanagement.RunRef{}, false, nil
	}
	run, ok := x.deps.repo.RunState(task.RunID)
	if !ok || domain.TerminalRunStatus(run.Status) {
		return workmanagement.RunRef{}, false, nil
	}
	return workmanagement.RunRef{ID: run.ID, WorkflowID: run.WorkflowID, Status: run.Status}, true, nil
}

// Dispatch reuses dispatchIssue, so Workflow resolution is
// mapping SOP → Issue binding → Playbook → project default.
func (x *workManagementExecution) Dispatch(ctx context.Context, req workmanagement.WorkItemDispatch) (workmanagement.RunRef, error) {
	task, err := dispatchIssue(x.deps, dispatchRequest{
		IssueID:       req.WorkItemID,
		WorkflowID:    req.WorkflowID,
		Rerun:         req.Rerun,
		AllowRerun:    req.AllowRerun,
		TriggerOrigin: req.TriggerOrigin,
	})
	if err != nil {
		return workmanagement.RunRef{}, err
	}
	return workmanagement.RunRef{ID: task.RunID, WorkflowID: task.WorkflowID, Status: task.Status}, nil
}

func (x *workManagementExecution) Pause(_ context.Context, runID string) error {
	return x.deps.engine.Pause(runID)
}

func (x *workManagementExecution) Cancel(_ context.Context, runID string) error {
	return x.deps.engine.Cancel(runID)
}
