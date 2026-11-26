package repo

import (
	"testing"
	"time"

	"agentflow/internal/domain"
)

// Projection lookups are the only bridge between event processing and the
// issues table: a Work Item is found by its stable external business key, by
// the platform record reference for deletions, and listed per connection for
// reconciliation.
func TestIssueExternalIdentityLookups(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	ref := &domain.ExternalRef{
		Source: domain.ManagementPlatformFeishuBitable, ExternalID: "rec-1",
		ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-1", RecordID: "rec-1",
	}
	if err := s.PutIssue(domain.Issue{ID: "wi-1", ProjectID: "platform", Title: "需求一", ExternalRef: ref}); err != nil {
		t.Fatal(err)
	}
	other := &domain.ExternalRef{
		Source: domain.ManagementPlatformFeishuBitable, ExternalID: "rec-2",
		ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-2", RecordID: "rec-2",
	}
	if err := s.PutIssue(domain.Issue{ID: "wi-2", ProjectID: "platform", Title: "需求二", ExternalRef: other}); err != nil {
		t.Fatal(err)
	}
	local := domain.Issue{ID: "local-1", ProjectID: "platform", Title: "本地"}
	if err := s.PutIssue(local); err != nil {
		t.Fatal(err)
	}

	got, ok := s.IssueByExternalKey("conn-1", "tbl-1", "REQ-1")
	if !ok || got.ID != "wi-1" || got.ExternalRef == nil || got.ExternalRef.RecordID != "rec-1" {
		t.Fatalf("by business key = %+v ok=%v", got, ok)
	}
	if _, ok := s.IssueByExternalKey("conn-1", "tbl-2", "REQ-1"); ok {
		t.Fatal("business key must be scoped to the table")
	}
	if _, ok := s.IssueByExternalKey("conn-1", "tbl-1", "REQ-404"); ok {
		t.Fatal("unknown business key must not resolve")
	}

	byRecord, ok := s.IssueByExternalRecordID("conn-1", "tbl-1", "rec-2")
	if !ok || byRecord.ID != "wi-2" {
		t.Fatalf("by record id = %+v ok=%v", byRecord, ok)
	}

	items, err := s.IssuesByExternalConnection("conn-1", "tbl-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("connection items = %d", len(items))
	}

	// An orphaned Work Item keeps its identity so reconciliation is idempotent.
	byRecord.ExternalRef.Orphaned = true
	byRecord.ExternalRef.OrphanedAt = time.Now().UTC()
	if err := s.PutIssue(byRecord); err != nil {
		t.Fatal(err)
	}
	orphaned, ok := s.IssueByExternalKey("conn-1", "tbl-1", "REQ-2")
	if !ok || orphaned.ExternalRef == nil || !orphaned.ExternalRef.Orphaned {
		t.Fatalf("orphan lookup = %+v ok=%v", orphaned, ok)
	}
}

func TestWorkManagementEventsAreClaimableAndDue(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	now := time.Now().UTC()
	event := domain.WorkManagementEvent{
		EventID: "evt-1", ConnectionID: "conn-1", TableID: "tbl-1", RecordID: "rec-1",
		Action: domain.ManagementEventRecordAdded, Status: domain.ManagementEventProcessing,
		ReceivedAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	created, err := s.RecordWorkManagementEvent(event)
	if err != nil || !created {
		t.Fatalf("record = %v %v", created, err)
	}
	if created, _ := s.RecordWorkManagementEvent(event); created {
		t.Fatal("duplicate event id must be ignored")
	}
	// A crash leaves the row in "processing"; age it so the startup reset
	// recognizes it as stranded.
	if err := s.DB.Model(&workManagementEventRow{}).Where("event_id = ?", "evt-1").
		Update("updated_at", now.Add(-2*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	if err := s.ResetWorkManagementEventsProcessing(now.Add(-30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	due, err := s.WorkManagementEventsDue(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Status != domain.ManagementEventPending {
		t.Fatalf("due after reset = %+v", due)
	}

	stored, ok := s.WorkManagementEventByEventID("evt-1")
	if !ok {
		t.Fatal("event missing")
	}
	stored.Status = domain.ManagementEventFailed
	stored.Attempts = 1
	stored.NextAttemptAt = now.Add(time.Hour)
	if err := s.UpdateWorkManagementEvent(stored); err != nil {
		t.Fatal(err)
	}
	due, err = s.WorkManagementEventsDue(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("future retry must not be due: %+v", due)
	}
	due, err = s.WorkManagementEventsDue(now.Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("retry due later = %+v %v", due, err)
	}
}
