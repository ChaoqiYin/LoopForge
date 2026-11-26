package repo

import (
	"testing"

	"agentflow/internal/domain"
)

func TestRunOriginPersistsAndClassifiesTemporary(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	scratch := domain.Run{
		ID: "run-scratch", Kind: "workspace", Status: "running",
		Origin:           domain.RunOriginAssistantScratch,
		WorkflowSnapshot: domain.WorkflowDefinition{ID: "conversation-workspace"},
	}
	if err := s.PutRun(scratch); err != nil {
		t.Fatal(err)
	}
	stored, ok := s.Run("run-scratch")
	if !ok || stored.Origin != domain.RunOriginAssistantScratch || !stored.IsTemporary() {
		t.Fatalf("scratch run origin = %+v, ok=%v", stored, ok)
	}

	formal := domain.Run{ID: "run-formal", Status: "running", WorkflowSnapshot: domain.WorkflowDefinition{ID: "delivery"}}
	if err := s.PutRun(formal); err != nil {
		t.Fatal(err)
	}
	stored, ok = s.Run("run-formal")
	if !ok || stored.Origin != "" || stored.IsTemporary() || !stored.IsFormal() {
		t.Fatalf("formal run origin = %+v, ok=%v", stored, ok)
	}
}
