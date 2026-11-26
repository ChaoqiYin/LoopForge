package workmanagement

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// This file turns an external business status *entry* into an execution
// action. It only runs after projection has persisted the new snapshot, and
// it never touches the local Issue.Status: the external business status and
// the internal execution lifecycle stay separate.

// statusSnapshot is the projection state before a record was applied. It is
// the basis for change detection, rollback detection, pending actions and
// writeback loop recognition.
type statusSnapshot struct {
	state          string
	history        []string
	writebackState string
	writebackAt    time.Time
	pending        *domain.WorkItemPendingAction
}

func snapshotStatus(issue domain.Issue) statusSnapshot {
	if issue.ExternalRef == nil {
		return statusSnapshot{}
	}
	ref := issue.ExternalRef
	return statusSnapshot{
		state:          strings.TrimSpace(ref.ExternalState),
		history:        append([]string(nil), ref.StatusHistory...),
		writebackState: strings.TrimSpace(ref.WritebackState),
		writebackAt:    ref.WritebackAt,
		pending:        issue.ExternalPendingAction,
	}
}

// afterProjection evaluates a freshly projected Work Item. A status entry is
// only acted on when the external value actually changed; a repeated event or
// reconciliation therefore never dispatches twice. An unchanged projection may
// still drain a pending action once the Work Item is idle.
func (s *Service) afterProjection(ctx context.Context, conn domain.ManagementConnection, issue domain.Issue, before statusSnapshot) error {
	if issue.ExternalRef == nil || issue.ExternalRef.Orphaned {
		return nil
	}
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()

	current := issue.ExternalBusinessStatus()
	if current == before.state {
		return s.applyPendingAction(ctx, conn, issue, before)
	}
	if current == "" {
		return nil
	}
	// A writeback changes the projection optimistically. If an event for the
	// write still arrives (reordered delivery, another instance), recognize it
	// by the marker instead of treating it as a new business transition.
	if isSelfWriteback(before, current, s.now()) {
		return s.consumeWritebackMarker(issue.ID, current)
	}
	// Regressions are recorded, never reversed automatically.
	if statusSeen(before.history, current) {
		return s.recordStatusOnly(issue.ID, operationStatusRollback, "外部状态回退，仅记录："+current)
	}
	mappings, err := s.store.ManagementStatusMappings(conn.ID, conn.TableID)
	if err != nil {
		return err
	}
	mapping, ok := findStatusMapping(mappings, current)
	if !ok {
		// An unmapped status is synchronized for display but never executes.
		return s.recordStatusOnly(issue.ID, operationStatusObserved, "未配置状态映射，仅同步："+current)
	}
	return s.applyStatusMapping(ctx, conn, issue, mapping)
}

// applyPendingAction drains the action deferred while the Work Item had an
// active Run. Only an idle Work Item consumes it.
func (s *Service) applyPendingAction(ctx context.Context, conn domain.ManagementConnection, issue domain.Issue, before statusSnapshot) error {
	if before.pending == nil {
		return nil
	}
	// Only dispatch-like actions wait for the Work Item to become idle.
	// Writeback and Run control are independent of the active Run.
	if before.pending.Action == domain.ManagementActionStartSOP || before.pending.Action == domain.ManagementActionRestart {
		_, active, err := s.exec.ActiveRun(ctx, issue.ID)
		if err != nil {
			return err
		}
		if active {
			return nil
		}
	}
	return s.applyStatusMapping(ctx, conn, issue, domain.ManagementStatusMapping{
		ConnectionID: conn.ID, TableID: conn.TableID,
		ExternalStatus:  before.pending.ExternalStatus,
		Action:          before.pending.Action,
		WorkflowID:      before.pending.WorkflowID,
		WritebackStatus: before.pending.WritebackStatus,
	})
}

// executionEnabled reports whether actions can reach an execution side. With
// no execution side the projection and audit still work, but no action is
// claimed as applied.
func (s *Service) executionEnabled() bool {
	_, disabled := s.exec.(noopExecution)
	return !disabled
}

func (s *Service) applyStatusMapping(ctx context.Context, conn domain.ManagementConnection, issue domain.Issue, mapping domain.ManagementStatusMapping) error {
	switch mapping.Action {
	case domain.ManagementActionIgnore:
		return s.completeAction(issue.ID, "状态映射为忽略："+mapping.ExternalStatus)
	case domain.ManagementActionWriteback:
		return s.writebackStatus(ctx, conn, issue, mapping)
	case domain.ManagementActionStartSOP:
		if !s.executionEnabled() {
			return nil
		}
		_, active, err := s.exec.ActiveRun(ctx, issue.ID)
		if err != nil {
			return err
		}
		if active {
			// One active Run per Work Item. Keep the latest intent; a later
			// idle projection drains it.
			return s.deferAction(issue.ID, mapping)
		}
		return s.dispatchAction(ctx, issue, mapping, false)
	case domain.ManagementActionPause:
		if !s.executionEnabled() {
			return nil
		}
		return s.controlRun(ctx, issue, mapping, false)
	case domain.ManagementActionCancel:
		if !s.executionEnabled() {
			return nil
		}
		return s.controlRun(ctx, issue, mapping, true)
	case domain.ManagementActionRestart:
		if !s.executionEnabled() {
			return nil
		}
		return s.restartAction(ctx, issue, mapping)
	}
	return nil
}

func (s *Service) dispatchAction(ctx context.Context, issue domain.Issue, mapping domain.ManagementStatusMapping, restart bool) error {
	ref, err := s.exec.Dispatch(ctx, WorkItemDispatch{
		WorkItemID: issue.ID, WorkflowID: mapping.WorkflowID,
		ExternalStatus: mapping.ExternalStatus, Rerun: restart, AllowRerun: true,
		TriggerOrigin: statusTriggerOrigin,
	})
	if err != nil {
		_ = s.appendSystemOperation(issue.ID, operationActionFailed, "外部状态动作失败："+UserMessage(err))
		// A failed action must be retried even though the status snapshot is
		// already stored; an unchanged projection drains this pending action.
		_ = s.deferAction(issue.ID, mapping)
		return err
	}
	detail := "外部状态触发：" + actionLabel(mapping)
	if ref.ID != "" {
		detail += "，Run " + ref.ID
	}
	return s.completeAction(issue.ID, detail)
}

// controlRun applies an explicit pause/cancel. It never interrupts a Run for a
// start_sop entry; those are deferred instead.
func (s *Service) controlRun(ctx context.Context, issue domain.Issue, mapping domain.ManagementStatusMapping, cancel bool) error {
	ref, active, err := s.exec.ActiveRun(ctx, issue.ID)
	if err != nil {
		return err
	}
	if !active {
		return s.skipAction(issue.ID, "没有运行中的 Run，忽略："+actionLabel(mapping))
	}
	if cancel {
		err = s.exec.Cancel(ctx, ref.ID)
	} else {
		err = s.exec.Pause(ctx, ref.ID)
	}
	if err != nil {
		_ = s.appendSystemOperation(issue.ID, operationActionFailed, "外部状态动作失败："+UserMessage(err))
		_ = s.deferAction(issue.ID, mapping)
		return err
	}
	return s.completeAction(issue.ID, actionLabel(mapping)+"，Run "+ref.ID)
}

// restartAction cancels the active Run first, then dispatches the SOP again.
func (s *Service) restartAction(ctx context.Context, issue domain.Issue, mapping domain.ManagementStatusMapping) error {
	ref, active, err := s.exec.ActiveRun(ctx, issue.ID)
	if err != nil {
		return err
	}
	if active {
		if err := s.exec.Cancel(ctx, ref.ID); err != nil {
			_ = s.appendSystemOperation(issue.ID, operationActionFailed, "重启前取消旧 Run 失败："+UserMessage(err))
			_ = s.deferAction(issue.ID, mapping)
			return err
		}
	}
	return s.dispatchAction(ctx, issue, mapping, true)
}

// writebackStatus performs the only external write a status mapping may
// request. ExternalState is an authoritative read-only snapshot: it is never
// advanced optimistically here. Only a subsequent event or reconciliation
// read-back updates it. The writeback marker only lets the projection
// recognize its own write when that event arrives.
func (s *Service) writebackStatus(ctx context.Context, conn domain.ManagementConnection, issue domain.Issue, mapping domain.ManagementStatusMapping) error {
	ref := issue.ExternalRef
	if ref == nil {
		return nil
	}
	target := strings.TrimSpace(mapping.WritebackStatus)
	if target == "" {
		return nil
	}
	if err := s.adapter.UpdateExternalStatus(ctx, conn, *ref, target); err != nil {
		switch {
		case errors.Is(err, ErrWritebackDisabled), errors.Is(err, ErrWritebackPaused),
			errors.Is(err, ErrReadOnlyTable), errors.Is(err, ErrFieldMapping), errors.Is(err, ErrFeishuPermission):
			return s.skipAction(issue.ID, "外部状态回写跳过："+UserMessage(err))
		}
		_ = s.appendSystemOperation(issue.ID, operationActionFailed, "外部状态回写失败："+UserMessage(err))
		// The outbox retry path: keep the write as a pending action so an
		// unchanged projection retries it without blocking the Run.
		_ = s.deferAction(issue.ID, mapping)
		return err
	}
	_, err := s.patchIssue(issue.ID, func(item *domain.Issue) error {
		if item.ExternalRef != nil {
			item.ExternalRef.WritebackState = target
			item.ExternalRef.WritebackAt = s.now()
		}
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: operationWriteback, Detail: "回写外部状态：" + target})
		return nil
	})
	return err
}

func (s *Service) deferAction(issueID string, mapping domain.ManagementStatusMapping) error {
	now := s.now()
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		item.ExternalPendingAction = &domain.WorkItemPendingAction{
			Action: mapping.Action, ExternalStatus: mapping.ExternalStatus,
			WorkflowID: mapping.WorkflowID, WritebackStatus: mapping.WritebackStatus, At: now,
		}
		item.AppendOperation(domain.IssueOperation{At: now, Actor: systemActor, Action: operationActionPending, Detail: "运行中，待执行：" + actionLabel(mapping)})
		return nil
	})
	return err
}

func (s *Service) completeAction(issueID, detail string) error {
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		item.ExternalPendingAction = nil
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: operationActionApplied, Detail: detail})
		return nil
	})
	return err
}

// skipAction records a decision that needs no retry and clears any deferred
// action for the same external state.
func (s *Service) skipAction(issueID, detail string) error {
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		item.ExternalPendingAction = nil
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: operationActionSkipped, Detail: detail})
		return nil
	})
	return err
}

func (s *Service) consumeWritebackMarker(issueID, current string) error {
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		if item.ExternalRef != nil {
			item.ExternalRef.WritebackState = ""
			item.ExternalRef.WritebackAt = time.Time{}
		}
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: operationWritebackIgnored, Detail: "识别为本系统回写，不触发动作：" + current})
		return nil
	})
	return err
}

func (s *Service) appendSystemOperation(issueID, action, detail string) error {
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: action, Detail: detail})
		return nil
	})
	return err
}

// recordStatusOnly writes an audit observation that must not execute. A
// dispatch-like pending action is dropped because the external state moved on;
// a pending writeback keeps its outbox retry.
func (s *Service) recordStatusOnly(issueID, action, detail string) error {
	_, err := s.patchIssue(issueID, func(item *domain.Issue) error {
		if item.ExternalPendingAction != nil && item.ExternalPendingAction.Action != domain.ManagementActionWriteback {
			item.ExternalPendingAction = nil
		}
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: action, Detail: detail})
		return nil
	})
	return err
}

func (s *Service) patchIssue(issueID string, fn func(*domain.Issue) error) (domain.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.PatchIssue(issueID, fn)
}

func actionLabel(mapping domain.ManagementStatusMapping) string {
	switch mapping.Action {
	case domain.ManagementActionStartSOP:
		return "启动 SOP：" + mapping.WorkflowID
	case domain.ManagementActionRestart:
		return "重启 SOP：" + mapping.WorkflowID
	case domain.ManagementActionPause:
		return "暂停 Run"
	case domain.ManagementActionCancel:
		return "取消 Run"
	case domain.ManagementActionWriteback:
		return "回写外部状态：" + mapping.WritebackStatus
	case domain.ManagementActionIgnore:
		return "忽略"
	}
	return string(mapping.Action)
}

func findStatusMapping(items []domain.ManagementStatusMapping, status string) (domain.ManagementStatusMapping, bool) {
	status = strings.TrimSpace(status)
	for _, item := range items {
		if strings.TrimSpace(item.ExternalStatus) == status {
			return item, true
		}
	}
	return domain.ManagementStatusMapping{}, false
}

func appendStatusHistory(history []string, status string) []string {
	status = strings.TrimSpace(status)
	if status == "" {
		return history
	}
	history = append(history, status)
	const maxHistory = 20
	if len(history) > maxHistory {
		history = append([]string(nil), history[len(history)-maxHistory:]...)
	}
	return history
}

func statusSeen(history []string, status string) bool {
	status = strings.TrimSpace(status)
	for _, item := range history {
		if strings.TrimSpace(item) == status {
			return true
		}
	}
	return false
}

func isSelfWriteback(before statusSnapshot, current string, now time.Time) bool {
	if before.writebackState == "" || before.writebackState != strings.TrimSpace(current) {
		return false
	}
	if before.writebackAt.IsZero() {
		return false
	}
	const writebackTTL = 5 * time.Minute
	return now.Sub(before.writebackAt) <= writebackTTL
}
