package repo

import (
	"testing"

	"agentflow/internal/domain"
)

func TestIssueExternalBusinessKeyIsUnique(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	// Local issues have no external identity and may coexist.
	for _, id := range []string{"local-1", "local-2"} {
		if err := s.PutIssue(domain.Issue{ID: id, ProjectID: "proj-1", Title: id}); err != nil {
			t.Fatalf("local issue %s: %v", id, err)
		}
	}

	ref := &domain.ExternalRef{
		Source: "feishu_bitable", ExternalID: "rec-1", ExternalState: "开发中",
		ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-1", RecordID: "rec-1",
	}
	if err := s.PutIssue(domain.Issue{ID: "wi-1", ProjectID: "proj-1", Title: "需求一", ExternalRef: ref}); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Issue("wi-1")
	if !ok || !got.FormalWorkItem() || got.ExternalBusinessStatus() != "开发中" {
		t.Fatalf("work item projection = %+v, ok=%v", got, ok)
	}
	if got.ExternalRef == nil || got.ExternalRef.ConnectionID != "conn-1" || got.ExternalRef.TableID != "tbl-1" ||
		got.ExternalRef.BusinessKey != "REQ-1" || got.ExternalRef.RecordID != "rec-1" {
		t.Fatalf("external business key lost: %+v", got.ExternalRef)
	}

	duplicate := &domain.ExternalRef{
		Source: "feishu_bitable", ExternalID: "rec-2", ExternalState: "开发中",
		ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-1", RecordID: "rec-2",
	}
	if err := s.PutIssue(domain.Issue{ID: "wi-2", ProjectID: "proj-1", Title: "需求二", ExternalRef: duplicate}); err == nil {
		t.Fatal("duplicate (connection, table, business_key) should be rejected")
	}

	// The same business key in another table is a different record.
	otherTable := &domain.ExternalRef{
		Source: "feishu_bitable", ExternalID: "rec-3", ConnectionID: "conn-1", TableID: "tbl-2", BusinessKey: "REQ-1", RecordID: "rec-3",
	}
	if err := s.PutIssue(domain.Issue{ID: "wi-3", ProjectID: "proj-1", Title: "需求三", ExternalRef: otherTable}); err != nil {
		t.Fatalf("same business key in another table: %v", err)
	}

	// Updating the same work item keeps its identity.
	got.Title = "需求一（更新）"
	if err := s.PutIssue(got); err != nil {
		t.Fatalf("update same work item: %v", err)
	}
	stored, _ := s.Issue("wi-1")
	if stored.Title != "需求一（更新）" {
		t.Fatalf("work item not updated: %+v", stored)
	}

	// Orphaned records keep their identity but stop being formal.
	stored.ExternalRef.Orphaned = true
	if err := s.PutIssue(stored); err != nil {
		t.Fatal(err)
	}
	orphaned, _ := s.Issue("wi-1")
	if orphaned.ExternalRef == nil || !orphaned.ExternalRef.Orphaned {
		t.Fatalf("orphan marker lost: %+v", orphaned.ExternalRef)
	}
}
