package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentflow/internal/domain"
)

// workManagementTriggerFields includes the acceptance criteria mapping the
// projection needs for execution input.
const workManagementTriggerFields = `{"fieldMappings":[
 {"field":"business_key","externalFieldName":"业务编号"},
 {"field":"title","externalFieldName":"标题"},
 {"field":"status","externalFieldName":"状态"},
 {"field":"description","externalFieldName":"描述"},
 {"field":"acceptance_criteria","externalFieldName":"验收标准"},
 {"field":"project","externalFieldName":"项目"},
 {"field":"repository","externalFieldName":"仓库"},
 {"field":"external_url","externalFieldName":"外链"}
]}`

func (s *workManagementEventTestServer) configureTrigger(t *testing.T, statusMappings string) string {
	t.Helper()
	connectionID := s.configure(t)
	rec := s.request(t, http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", workManagementTriggerFields)
	if rec.Code != http.StatusOK {
		t.Fatalf("trigger field mappings = %d %s", rec.Code, rec.Body.String())
	}
	rec = s.request(t, http.MethodPut, "/api/work-management/connections/"+connectionID+"/status-mappings", statusMappings)
	if rec.Code != http.StatusOK {
		t.Fatalf("status mappings = %d %s", rec.Code, rec.Body.String())
	}
	return connectionID
}

func (s *workManagementEventTestServer) putStartWorkflow(t *testing.T, id string) {
	t.Helper()
	if err := s.store.PutWorkflow(domain.WorkflowDefinition{
		ID: id, Name: id, Version: 1, SandboxProfileID: "test-native",
		Nodes: []domain.WorkflowNode{{ID: "approval", Name: "Approval", Type: "approval", MaxAttempts: 1}},
	}); err != nil {
		t.Fatal(err)
	}
}

func (s *workManagementEventTestServer) runsForIssue(t *testing.T, issueID string) []domain.Run {
	t.Helper()
	rec := s.request(t, http.MethodGet, "/api/runs?projectId=platform", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("runs API = %d %s", rec.Code, rec.Body.String())
	}
	var runs []domain.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode runs: %v (%s)", err, rec.Body.String())
	}
	out := make([]domain.Run, 0, len(runs))
	for _, run := range runs {
		if run.ExecutionContext.IssueID == issueID {
			out = append(out, run)
		}
	}
	return out
}

func (s *workManagementEventTestServer) pushRecordEvent(t *testing.T, eventID, recordID, action string, fields ...map[string]any) {
	t.Helper()
	s.fake.push(t, s.ws, recordChangedJSON(eventID, recordID, action, fields...))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("event ack = %d", code)
	}
}

func TestWorkManagementStatusEntryStartsSOP(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"}]}`)
	s.putStartWorkflow(t, "wm-start")
	if err := s.store.PutRepository(domain.Repository{
		ID: "wm-repo", ProjectID: "platform", Name: "wm-repo", LocalPath: gitRepo(t), DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	s.fake.setRecord("rec-1", map[string]any{
		"业务编号": "REQ-1", "标题": "实现登录", "状态": "开发中",
		"描述": "登录页报错", "验收标准": "可以通过登录", "仓库": "wm-repo",
		"外链": "https://example.com/req/1",
	})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "projected work item", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})

	var run domain.Run
	s.waitFor(t, "SOP run for the work item", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		if len(runs) == 0 {
			return false
		}
		run = runs[0]
		return true
	})

	if !strings.Contains(run.Input, "实现登录") || !strings.Contains(run.Input, "登录页报错") {
		t.Fatalf("run input missing work item title/description: %q", run.Input)
	}
	if !strings.Contains(run.Input, "可以通过登录") {
		t.Fatalf("run input missing acceptance criteria: %q", run.Input)
	}
	if !strings.Contains(run.Input, "https://example.com/req/1") {
		t.Fatalf("run input missing external link: %q", run.Input)
	}
	if run.Repository.RepositoryID != "wm-repo" || !strings.Contains(run.Input, "wm-repo") {
		t.Fatalf("run input missing repository: %+v %q", run.Repository, run.Input)
	}
	if issue.ExternalRef == nil || issue.ExternalRef.ExternalState != "开发中" {
		t.Fatalf("external status snapshot = %+v", issue.ExternalRef)
	}
	assertSystemAudit(t, issue, "external_action")
}

// assertSystemAudit proves an automated transition is visible as a system
// actor in the Work Item audit trail.
func assertSystemAudit(t *testing.T, issue domain.Issue, action string) {
	t.Helper()
	for _, op := range issue.Operations {
		if op.Action == action && op.Actor == "system" {
			return
		}
	}
	t.Fatalf("missing system audit %q in %+v", action, issue.Operations)
}

func TestWorkManagementStatusEntryDoesNotRedispatch(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configureTrigger(t, `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"}]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "只派发一次", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	s.waitFor(t, "first SOP run", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })

	// At-least-once delivery: the same event id is deduplicated.
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})
	// A different delivery carrying the same authoritative state must not
	// count as a status entry either.
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"开发中"`})
	// Reconciliation reads the same state again.
	s.fake.setSearch([]map[string]any{{
		"record_id": "rec-1", "last_modified_time": time.Now().UnixMilli(),
		"fields": map[string]any{"业务编号": "REQ-1", "标题": "只派发一次", "状态": "开发中"},
	}})
	if rec := s.request(t, http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", ""); rec.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", rec.Code, rec.Body.String())
	}

	time.Sleep(500 * time.Millisecond)
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 1 {
		t.Fatalf("duplicate deliveries dispatched %d runs, want 1", len(runs))
	}
}

func TestWorkManagementStatusChangeWhileRunningDefersAction(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"},
	 {"externalStatus":"评审中","action":"start_sop","workflowId":"wm-start"}
	]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "运行中变化", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	s.waitFor(t, "first SOP run", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })

	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "运行中变化", "状态": "评审中"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"评审中"`})

	s.waitFor(t, "projection and deferred action", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if !ok || found.ExternalRef == nil || found.ExternalRef.ExternalState != "评审中" {
			return false
		}
		issue = found
		return found.ExternalPendingAction != nil
	})
	if issue.ExternalPendingAction.Action != domain.ManagementActionStartSOP {
		t.Fatalf("pending action = %+v", issue.ExternalPendingAction)
	}
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 1 {
		t.Fatalf("status change during a run dispatched %d runs, want 1", len(runs))
	}
	if runs := s.runsForIssue(t, issue.ID); len(runs) == 1 && domain.TerminalRunStatus(runs[0].Status) {
		t.Fatalf("running status change interrupted the active run: %+v", runs[0])
	}
	assertSystemAudit(t, issue, "external_action_pending")
}

func TestWorkManagementRestartCancelsOldRunAndDispatches(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	// The restart mapping carries no SOP: resolution falls back to the Issue
	// binding created by the first dispatch.
	s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"},
	 {"externalStatus":"重跑","action":"restart"}
	]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "需要重跑", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	var firstRun domain.Run
	s.waitFor(t, "first SOP run", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		if len(runs) == 0 {
			return false
		}
		firstRun = runs[0]
		return true
	})

	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "需要重跑", "状态": "重跑"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"重跑"`})

	var runs []domain.Run
	s.waitFor(t, "restart dispatch", func() bool {
		runs = s.runsForIssue(t, issue.ID)
		return len(runs) == 2
	})
	oldCancelled, newActive := false, false
	for _, run := range runs {
		if run.ID == firstRun.ID {
			oldCancelled = run.Status == "cancelled"
			continue
		}
		newActive = !domain.TerminalRunStatus(run.Status) && run.WorkflowID == "wm-start"
	}
	if !oldCancelled || !newActive {
		t.Fatalf("restart semantics wrong: runs=%+v", runs)
	}
	stored, _ := s.issueByBusinessKey(t, "REQ-1")
	assertSystemAudit(t, stored, "external_action")
}

func (s *workManagementEventTestServer) enableWriteback(t *testing.T, connectionID string) {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"name":"飞书需求表","platform":"feishu_bitable","botId":"bot-feishu","baseAppToken":"bascn","tableId":"tbl-1","defaultProjectId":"platform","allowWriteback":true,"enabled":true}`, connectionID)
	if rec := s.request(t, http.MethodPut, "/api/work-management/connections", body); rec.Code != http.StatusOK {
		t.Fatalf("enable writeback = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWorkManagementWritebackIsExplicitAndDoesNotLoop(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"writeback","writebackStatus":"进行中"},
	 {"externalStatus":"进行中","action":"start_sop","workflowId":"wm-loop"}
	]}`)
	s.enableWriteback(t, connectionID)
	s.putStartWorkflow(t, "wm-loop")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "回写不循环", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "optimistic writeback snapshot", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if !ok {
			return false
		}
		issue = found
		return found.ExternalRef != nil && found.ExternalRef.ExternalState == "进行中"
	})
	assertSystemAudit(t, issue, "external_writeback")

	req, ok := s.fake.lastRequest("batch_update")
	if !ok {
		t.Fatal("writeback never reached the fake Feishu OpenAPI")
	}
	records, _ := req.Body["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("batch_update body = %+v", req.Body)
	}
	record, _ := records[0].(map[string]any)
	fields, _ := record["fields"].(map[string]any)
	if fields["状态"] != "进行中" {
		t.Fatalf("writeback fields = %+v", fields)
	}

	// The platform delivers the write as a record change. It must be
	// recognized as self-originated, not as a new business status entry.
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "回写不循环", "状态": "进行中"})
	s.pushRecordEvent(t, "evt-self", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"进行中"`})
	time.Sleep(500 * time.Millisecond)
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 0 {
		t.Fatalf("self writeback event triggered %d runs, want 0", len(runs))
	}
	stored, _ := s.issueByBusinessKey(t, "REQ-1")
	if count := operationCount(stored, "external_action"); count != 0 {
		t.Fatalf("self writeback event executed %d actions", count)
	}
}

func TestWorkManagementUnknownStatusOnlySyncs(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"}]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "未知状态", "状态": "待定"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "unmapped status sync", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if !ok {
			return false
		}
		issue = found
		return found.ExternalRef != nil && found.ExternalRef.ExternalState == "待定" && operationCount(found, "external_status_observed") == 1
	})
	time.Sleep(300 * time.Millisecond)
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 0 {
		t.Fatalf("unknown status triggered %d runs", len(runs))
	}

	// A configured status afterwards still dispatches.
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "未知状态", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"开发中"`})
	s.waitFor(t, "configured status dispatch", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })
}

func TestWorkManagementStatusRollbackIsRecordedOnly(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"}]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "状态回退", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	s.waitFor(t, "first SOP run", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })

	// Forward to an unconfigured state, then back to a state already seen.
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "状态回退", "状态": "评审中"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"评审中"`})
	s.waitFor(t, "forward status", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && found.ExternalRef != nil && found.ExternalRef.ExternalState == "评审中"
	})
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "状态回退", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-3", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"开发中"`})
	deadline := time.Now().Add(8 * time.Second)
	for {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok && found.ExternalRef != nil && found.ExternalRef.ExternalState == "开发中" && operationCount(found, "external_status_rollback") == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollback audit missing: ref=%+v ops=%+v", found.ExternalRef, found.Operations)
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 1 {
		t.Fatalf("rollback dispatched %d runs, want 1", len(runs))
	}
}

func operationCount(issue domain.Issue, action string) int {
	count := 0
	for _, op := range issue.Operations {
		if op.Action == action {
			count++
		}
	}
	return count
}

func TestWorkManagementCancelActionStopsActiveRun(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"},
	 {"externalStatus":"取消","action":"cancel"}
	]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "取消执行", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	s.waitFor(t, "first SOP run", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })

	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "取消执行", "状态": "取消"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"取消"`})

	s.waitFor(t, "cancelled run", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		return len(runs) == 1 && runs[0].Status == "cancelled"
	})
	stored, _ := s.issueByBusinessKey(t, "REQ-1")
	assertSystemAudit(t, stored, "external_action")
}

func TestWorkManagementPauseActionPausesActiveRun(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"},
	 {"externalStatus":"暂停","action":"pause"}
	]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "暂停执行", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	s.waitFor(t, "first SOP run", func() bool { return len(s.runsForIssue(t, issue.ID)) == 1 })

	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "暂停执行", "状态": "暂停"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"暂停"`})

	s.waitFor(t, "pause request", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		return len(runs) == 1 && runs[0].PauseRequested
	})
	stored, _ := s.issueByBusinessKey(t, "REQ-1")
	assertSystemAudit(t, stored, "external_action")
}

func TestWorkManagementPendingActionRunsWhenWorkItemIsIdle(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configureTrigger(t, `{"statusMappings":[
	 {"externalStatus":"开发中","action":"start_sop","workflowId":"wm-start"},
	 {"externalStatus":"评审中","action":"start_sop","workflowId":"wm-start"}
	]}`)
	s.putStartWorkflow(t, "wm-start")
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "待执行动作", "状态": "开发中"})
	s.pushRecordEvent(t, "evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`})

	var issue domain.Issue
	s.waitFor(t, "work item projection", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		if ok {
			issue = found
		}
		return ok
	})
	var firstRun domain.Run
	s.waitFor(t, "first SOP run", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		if len(runs) == 0 {
			return false
		}
		firstRun = runs[0]
		return true
	})

	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "待执行动作", "状态": "评审中"})
	s.pushRecordEvent(t, "evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-状态", "field_value": `"评审中"`})
	s.waitFor(t, "deferred action", func() bool {
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && found.ExternalPendingAction != nil
	})
	if runs := s.runsForIssue(t, issue.ID); len(runs) != 1 {
		t.Fatalf("deferred action dispatched early: %d runs", len(runs))
	}

	// Stop the active Run, then a reconciliation sees the same state and
	// drains the deferred action.
	if rec := s.request(t, http.MethodPost, "/api/runs/"+firstRun.ID+"/cancel", ""); rec.Code != http.StatusOK {
		t.Fatalf("cancel run = %d %s", rec.Code, rec.Body.String())
	}
	s.fake.setSearch([]map[string]any{{
		"record_id": "rec-1", "last_modified_time": time.Now().UnixMilli(),
		"fields": map[string]any{"业务编号": "REQ-1", "标题": "待执行动作", "状态": "评审中"},
	}})
	if rec := s.request(t, http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", ""); rec.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", rec.Code, rec.Body.String())
	}
	s.waitFor(t, "drained pending action", func() bool {
		runs := s.runsForIssue(t, issue.ID)
		if len(runs) != 2 {
			return false
		}
		found, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && found.ExternalPendingAction == nil
	})
}
