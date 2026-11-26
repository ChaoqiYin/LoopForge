package httpapi

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/engine"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
)

func newClosureTestServer(t *testing.T) (*repo.Store, http.Handler, *engine.Engine) {
	t.Helper()
	t.Setenv("AGENTFLOW_SESSION_SECRET", "local-issue-closure-session-secret")
	root := t.TempDir()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := engine.New(s, runtime.NewManager(filepath.Join(root, "workspaces")))
	return s, New(s, e, tracker.NewService(), "", ""), e
}

func externalWorkItem(id string) domain.Issue {
	return domain.Issue{
		ID: id, ProjectID: "platform", Title: "外部同步工作项", Status: "todo", Kind: "requirement",
		Source: "feishu_bitable",
		ExternalRef: &domain.ExternalRef{
			Source: "feishu_bitable", ExternalID: "rec-" + id, BusinessKey: "REQ-" + id,
			ConnectionID: "conn-1", TableID: "tbl-1", ExternalState: "待处理",
		},
	}
}

func TestLocalIssueCreationAndEditAreClosed(t *testing.T) {
	s, h, _ := newClosureTestServer(t)
	if err := s.PutIssue(domain.Issue{ID: "iss-local", ProjectID: "platform", Title: "本地需求"}); err != nil {
		t.Fatal(err)
	}

	create := requestJSON[map[string]string](t, h, http.MethodPost, "/api/issues", map[string]any{
		"projectId": "platform", "title": "新需求",
	}, http.StatusMethodNotAllowed)
	if !strings.Contains(create["error"], "本地建单已关闭") {
		t.Fatalf("create error = %q", create["error"])
	}
	if _, ok := s.Issue("本地需求"); ok {
		t.Fatal("rejected creation must not write an issue")
	}

	edit := requestJSON[map[string]string](t, h, http.MethodPut, "/api/issues", map[string]any{
		"id": "iss-local", "projectId": "platform", "title": "改标题",
	}, http.StatusMethodNotAllowed)
	if !strings.Contains(edit["error"], "只读") {
		t.Fatalf("edit error = %q", edit["error"])
	}
	stored, _ := s.Issue("iss-local")
	if stored.Title != "本地需求" {
		t.Fatalf("rejected edit changed the issue: %+v", stored)
	}

	requestEmptyJSON[[]domain.Issue](t, h, http.MethodGet, "/api/issues", http.StatusOK)
}

func TestClarifyRequiresFormalWorkItem(t *testing.T) {
	s, h, _ := newClosureTestServer(t)
	if err := s.PutIssue(domain.Issue{ID: "iss-local", ProjectID: "platform", Title: "本地需求"}); err != nil {
		t.Fatal(err)
	}
	requestJSON[map[string]string](t, h, http.MethodPost, "/api/issues/iss-local/clarification", map[string]any{
		"summary": "结论", "ready": true,
	}, http.StatusConflict)

	if err := s.PutIssue(externalWorkItem("iss-external")); err != nil {
		t.Fatal(err)
	}
	updated := requestJSON[domain.Issue](t, h, http.MethodPost, "/api/issues/iss-external/clarification", map[string]any{
		"summary": "外部工作项澄清结论", "ready": true,
	}, http.StatusOK)
	if updated.Clarification.Summary != "外部工作项澄清结论" || updated.Clarification.Status != domain.ClarificationReady {
		t.Fatalf("external work item clarification = %+v", updated.Clarification)
	}
}

func TestIssuesListCanSelectOnlySyncedWorkItems(t *testing.T) {
	s, h, _ := newClosureTestServer(t)
	if err := s.PutIssue(domain.Issue{ID: "iss-local", ProjectID: "platform", Title: "本地需求"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssue(externalWorkItem("iss-external")); err != nil {
		t.Fatal(err)
	}
	items := requestEmptyJSON[[]domain.Issue](t, h, http.MethodGet, "/api/issues?formal=1", http.StatusOK)
	if len(items) != 1 || items[0].ID != "iss-external" {
		t.Fatalf("formal filter = %+v", items)
	}
}

func TestRoomDispatchRequiresSyncedWorkItem(t *testing.T) {
	s, h, e := newClosureTestServer(t)
	if err := s.PutWorkflow(domain.WorkflowDefinition{
		ID: "closure-room-sop", Name: "群聊交付", Version: 1, ProjectID: "platform", Status: "published",
		SandboxProfileID: "test-native",
		Nodes:            []domain.WorkflowNode{{ID: "approval", Name: "人工确认", Type: "approval", MaxAttempts: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.User(1)
	room := requestJSONAuthed[domain.ChatRoomView](t, h, owner, http.MethodPost, "/api/rooms", map[string]any{
		"projectId": "platform", "name": "外部工作项群",
	}, http.StatusCreated)

	requestJSONAuthed[map[string]string](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"title": "不再本地建单", "workflowId": "closure-room-sop",
	}, http.StatusBadRequest)

	if err := s.PutIssue(domain.Issue{ID: "iss-local", ProjectID: "platform", Title: "本地需求"}); err != nil {
		t.Fatal(err)
	}
	requestJSONAuthed[map[string]string](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"issueId": "iss-local", "workflowId": "closure-room-sop",
	}, http.StatusConflict)

	if err := s.PutIssue(externalWorkItem("iss-external")); err != nil {
		t.Fatal(err)
	}
	got := requestJSONAuthed[roomIssueResult](t, h, owner, http.MethodPost, "/api/rooms/"+room.ID+"/issues", map[string]any{
		"issueId": "iss-external", "workflowId": "closure-room-sop",
	}, http.StatusCreated)
	if got.Issue.ID != "iss-external" || got.Message.IssueID != "iss-external" {
		t.Fatalf("room dispatch = %+v", got)
	}
	t.Cleanup(func() { _ = e.Cancel(got.Message.RunID) })
}

func TestSpaceRequirementLinksSyncedWorkItem(t *testing.T) {
	s, h, _ := newClosureTestServer(t)
	owner, _ := s.User(1)
	space := requestJSONAuthed[spaceView](t, h, owner, http.MethodPost, "/api/spaces", map[string]any{
		"projectId": "platform", "title": "外部需求会话",
	}, http.StatusCreated)

	requestJSONAuthed[map[string]string](t, h, owner, http.MethodPost, "/api/spaces/"+space.ID+"/requirement", map[string]any{
		"title": "本地新需求",
	}, http.StatusBadRequest)
	if issue, ok := s.Issue("iss-local"); ok {
		t.Fatalf("space requirement must not create an issue: %+v", issue)
	}

	if err := s.PutIssue(domain.Issue{ID: "iss-local", ProjectID: "platform", Title: "本地需求"}); err != nil {
		t.Fatal(err)
	}
	requestJSONAuthed[map[string]string](t, h, owner, http.MethodPost, "/api/spaces/"+space.ID+"/requirement", map[string]any{
		"issueId": "iss-local",
	}, http.StatusConflict)

	if err := s.PutIssue(externalWorkItem("iss-external")); err != nil {
		t.Fatal(err)
	}
	linked := requestJSONAuthed[spaceView](t, h, owner, http.MethodPost, "/api/spaces/"+space.ID+"/requirement", map[string]any{
		"issueId": "iss-external",
	}, http.StatusCreated)
	if linked.IssueID != "iss-external" || linked.Requirement == nil || linked.Requirement.ID != "iss-external" {
		t.Fatalf("space linked requirement = %+v", linked)
	}
}

func TestRunStartMarksAssistantScratch(t *testing.T) {
	s, h, e := newClosureTestServer(t)
	if err := s.PutWorkflow(domain.WorkflowDefinition{
		ID: "closure-scratch-sop", Name: "临时探索", Version: 1, ProjectID: "platform", Status: "published",
		SandboxProfileID: "test-native",
		Nodes:            []domain.WorkflowNode{{ID: "approval", Name: "人工确认", Type: "approval", MaxAttempts: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	run := requestJSON[domain.Run](t, h, http.MethodPost, "/api/runs", map[string]any{
		"workflowId": "closure-scratch-sop", "projectId": "platform", "input": "探索一下",
	}, http.StatusCreated)
	t.Cleanup(func() { _ = e.Cancel(run.ID) })
	if run.Origin != domain.RunOriginAssistantScratch || !run.IsTemporary() {
		t.Fatalf("started run origin = %q (%+v)", run.Origin, run)
	}
}

func TestScheduledToolsCannotCreateLocalIssues(t *testing.T) {
	for _, name := range agentflowScheduledToolNames() {
		if name == "issues_save" {
			t.Fatal("scheduled AI tasks must not create local issues")
		}
	}
	if _, ok := assistantPublicOperations["issues_save"]; ok {
		t.Fatal("issues_save must not be a platform tool")
	}
}
