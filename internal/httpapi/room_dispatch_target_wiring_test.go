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

// TestRoomDispatchTargetReachesIssueAndCards pins the two callers of
// roomDispatchTarget: every dispatch card must carry the same multi-SOP
// target, keyed by the first SOP, and a single-SOP dispatch onto an existing
// Work Item must stay an "sop" target.
func TestRoomDispatchTargetReachesIssueAndCards(t *testing.T) {
	t.Setenv("AGENTFLOW_SESSION_SECRET", "room-test-session-secret")
	root := t.TempDir()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, wf := range []domain.WorkflowDefinition{
		{ID: "target-build", Name: "构建"},
		{ID: "target-review", Name: "评审"},
		{ID: "target-ship", Name: "上线"},
	} {
		wf.Version, wf.ProjectID, wf.Status, wf.SandboxProfileID = 1, "platform", "published", "test-native"
		wf.Nodes = []domain.WorkflowNode{{ID: "approval", Name: "人工确认", Type: "approval", MaxAttempts: 1}}
		if err := s.PutWorkflow(wf); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutIssue(domain.Issue{
		ID: "room-target-issue", ProjectID: "platform", Title: "三条流程并行", Status: "todo", WorkflowID: "target-build", Source: "feishu_bitable",
		ExternalRef: &domain.ExternalRef{Source: "feishu_bitable", ExternalID: "rec-room-target"},
	}); err != nil {
		t.Fatal(err)
	}
	e := engine.New(s, runtime.NewManager(filepath.Join(root, "workspaces")))
	h := New(s, e, tracker.NewService(), "", "")
	owner, _ := s.User(1)
	room := requestJSONAuthed[domain.ChatRoomView](t, h, owner, http.MethodPost, "/api/rooms", map[string]any{
		"projectId": "platform", "name": "派发目标群",
	}, http.StatusCreated)
	cancelRuns := func(res roomIssueResult) {
		for _, task := range res.Tasks {
			runID := task.RunID
			t.Cleanup(func() { _ = e.Cancel(runID) })
		}
	}

	const multiName = "SOP「构建」、「评审」、「上线」"
	multi := requestJSONAuthed[roomIssueResult](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"issueId": "room-target-issue", "workflowIds": []string{"target-build", "target-review", "target-ship"},
	}, http.StatusCreated)
	cancelRuns(multi)
	if multi.Issue.ID != "room-target-issue" || multi.Issue.WorkflowID != "target-build" {
		t.Fatalf("dispatch should reuse the synced Work Item: %+v", multi.Issue)
	}
	stored, ok := s.Issue(multi.Issue.ID)
	if !ok || len(stored.Operations) != 3 {
		t.Fatalf("issue should record one dispatch per SOP: %+v %v", stored, ok)
	}
	for i, op := range stored.Operations {
		if op.Action != "dispatch" {
			t.Fatalf("operation %d = %+v", i, op)
		}
	}
	if stored.Operations[0].Detail != "协作群派发 SOP「构建」" || stored.Operations[2].Detail != "协作群派发 SOP「上线」" {
		t.Fatalf("dispatch operations should name their own SOP: %+v", stored.Operations)
	}
	if len(multi.Messages) != 3 {
		t.Fatalf("one dispatch card per SOP: %+v", multi.Messages)
	}
	for i, msg := range multi.Messages {
		if msg.TargetType != "multi" || msg.TargetID != "target-build" || msg.TargetName != multiName {
			t.Fatalf("card %d target = (%q, %q, %q)", i, msg.TargetType, msg.TargetID, msg.TargetName)
		}
	}

	// Dispatching again records more dispatch operations and the cards still
	// describe the target the same way.
	again := requestJSONAuthed[roomIssueResult](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"issueId": multi.Issue.ID, "workflowIds": []string{"target-ship", "target-review"},
	}, http.StatusCreated)
	cancelRuns(again)
	if len(again.Messages) != 2 {
		t.Fatalf("one card per SOP on the existing issue: %+v", again.Messages)
	}
	for i, msg := range again.Messages {
		if msg.TargetType != "multi" || msg.TargetID != "target-ship" || msg.TargetName != "SOP「上线」、「评审」" {
			t.Fatalf("existing-issue card %d target = (%q, %q, %q)", i, msg.TargetType, msg.TargetID, msg.TargetName)
		}
	}
	single := requestJSONAuthed[roomIssueResult](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"issueId": multi.Issue.ID, "workflowId": "target-review",
	}, http.StatusCreated)
	cancelRuns(single)
	if msg := single.Message; msg.TargetType != "sop" || msg.TargetID != "target-review" || msg.TargetName != "SOP「评审」" {
		t.Fatalf("single-SOP card target = (%q, %q, %q)", msg.TargetType, msg.TargetID, msg.TargetName)
	}
	latest, _ := s.Issue(multi.Issue.ID)
	creates := 0
	for _, op := range latest.Operations {
		if op.Action == "create" {
			creates++
		}
	}
	if creates != 0 {
		t.Fatalf("room dispatch must not create local issues: %+v", latest.Operations)
	}
}
