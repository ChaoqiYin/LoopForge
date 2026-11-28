package repo

import (
	"testing"
	"time"

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

func TestAnalyticsExcludesTemporaryRunsByDefault(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	wf := domain.WorkflowDefinition{ID: "closure-analytics", Name: "Analytics"}
	now := time.Now().UTC()
	for _, run := range []domain.Run{
		{ID: "run-formal", WorkflowID: wf.ID, WorkflowSnapshot: wf, Status: "succeeded", ExecutionContext: domain.TelemetryContext{ProjectID: "platform"}, CreatedAt: now, UpdatedAt: now},
		{ID: "run-scratch", WorkflowID: wf.ID, WorkflowSnapshot: wf, Status: "succeeded", Origin: domain.RunOriginAssistantScratch, ExecutionContext: domain.TelemetryContext{ProjectID: "platform"}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := s.PutRun(run); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Analytics(domain.AnalyticsFilter{ProjectID: "platform"})
	if got.Summary.TotalRuns != 1 || got.Summary.SucceededRuns != 1 {
		t.Fatalf("temporary runs must not enter formal statistics: %+v", got.Summary)
	}
}
