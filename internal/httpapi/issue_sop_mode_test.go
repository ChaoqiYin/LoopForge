package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/engine"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
)

func TestRequirementWithoutSOP(t *testing.T) {
	st, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.PutWorkflow(domain.WorkflowDefinition{ID: "sop-mode-flow", Name: "SOP mode", Version: 1, SandboxProfileID: "test-native", Nodes: []domain.WorkflowNode{{ID: "approval", Name: "Approval", Type: "approval", MaxAttempts: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProject(domain.Project{ID: "sop-mode", Name: "SOP mode", OwnerID: 1, WorkflowID: "sop-mode-flow"}); err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, runtime.NewManager(filepath.Join(t.TempDir(), "workspaces")))
	h := New(st, e, tracker.NewService(), "", "")

	if err := st.PutIssue(domain.Issue{ID: "sop-mode-bound", ProjectID: "sop-mode", Title: "默认绑定", Kind: "requirement", WorkflowID: "sop-mode-flow",
		ExternalRef: &domain.ExternalRef{Source: "feishu_bitable", ExternalID: "bound"}}); err != nil {
		t.Fatal(err)
	}

	created := domain.Issue{ID: "sop-mode-none", ProjectID: "sop-mode", Title: "不走 SOP", Kind: "requirement",
		SOPMode: domain.IssueSOPNone, AutomationState: "ignored", ExternalRef: &domain.ExternalRef{Source: "feishu_bitable", ExternalID: "none"}}
	if err := st.PutIssue(created); err != nil {
		t.Fatal(err)
	}

	d := &deps{repo: st, engine: e}
	if _, err := dispatchIssue(d, dispatchRequest{IssueID: created.ID, ScheduleJobID: "job-1"}); cvBHTTPStatusRound2(err) != http.StatusConflict {
		t.Fatalf("scheduled SOPs must not claim a no-SOP requirement: %v", err)
	}
	requestJSON[map[string]any](t, h, http.MethodPost, "/api/issues/"+created.ID+"/dispatch", map[string]any{}, http.StatusBadRequest)
	if _, err := dispatchIssue(d, dispatchRequest{IssueID: created.ID, WorkflowID: "sop-mode-missing"}); err == nil {
		t.Fatal("a missing SOP started")
	}
	failed, _ := st.Issue(created.ID)
	if failed.SOPMode != domain.IssueSOPNone || failed.WorkflowID != "" || failed.AutomationState != "ignored" {
		t.Fatalf("a failed start must keep the no-SOP choice: %+v", failed)
	}

	task := requestJSON[domain.Task](t, h, http.MethodPost, "/api/issues/"+created.ID+"/dispatch", map[string]any{"workflowId": "sop-mode-flow"}, http.StatusCreated)
	t.Cleanup(func() { _ = e.Cancel(task.RunID) })
	stored, _ := st.Issue(created.ID)
	if task.WorkflowID != "sop-mode-flow" || stored.SOPMode != "" || stored.WorkflowID != "sop-mode-flow" || stored.AutomationState != "eligible" {
		t.Fatalf("running a chosen SOP must switch the requirement to SOP delivery: task=%+v issue=%+v", task, stored)
	}

	other := domain.Issue{ID: "sop-mode-other", ProjectID: "sop-mode", Title: "稍后绑定", Kind: "requirement", SOPMode: domain.IssueSOPNone,
		ExternalRef: &domain.ExternalRef{Source: "feishu_bitable", ExternalID: "other"}}
	if err := st.PutIssue(other); err != nil {
		t.Fatal(err)
	}
	rebound := requestJSON[domain.Issue](t, h, http.MethodPut, "/api/issues/"+other.ID+"/sops", map[string]any{
		"bindings": []map[string]any{{"workflowId": "sop-mode-flow", "order": 1}},
	}, http.StatusOK)
	if rebound.SOPMode != "" || rebound.WorkflowID != "sop-mode-flow" || rebound.AutomationState != "eligible" {
		t.Fatalf("binding an SOP must switch the requirement to SOP delivery: %+v", rebound)
	}
}
