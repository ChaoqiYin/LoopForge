package repo

import (
	"testing"
	"time"

	"agentflow/internal/domain"
)

func TestDeliveryRecordsCRUD(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	now := time.Now().UTC().Truncate(time.Second)
	delivery := domain.Delivery{
		ID: "delivery-1", WorkItemID: "issue-1", ProjectID: "proj-1",
		RepositoryID: "repo-1", RepositoryName: "api", Branch: "work/req-1", CommitSHA: "abc123",
		Tests: domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: "全部通过"},
		State: domain.DeliveryStateRunning, CreatedAt: now,
	}
	if err := s.PutDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Delivery("delivery-1")
	if !ok || got.WorkItemID != "issue-1" || got.RepositoryID != "repo-1" || got.CommitSHA != "abc123" ||
		got.Tests.State != domain.DeliveryCheckPassed || got.State != domain.DeliveryStateRunning {
		t.Fatalf("delivery round trip = %+v, ok=%v", got, ok)
	}
	list, err := s.DeliveriesByWorkItem("issue-1")
	if err != nil || len(list) != 1 || list[0].ID != "delivery-1" {
		t.Fatalf("deliveries by work item = %+v, %v", list, err)
	}
	if other, err := s.DeliveriesByWorkItem("issue-2"); err != nil || len(other) != 0 {
		t.Fatalf("unrelated work item deliveries = %+v, %v", other, err)
	}

	delivery.State = domain.DeliveryStateSucceeded
	delivery.Review = domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: "审查通过"}
	delivery.CompletedAt = now.Add(time.Minute)
	if err := s.PutDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Delivery("delivery-1")
	if got.State != domain.DeliveryStateSucceeded || got.Review.Summary != "审查通过" || got.CompletedAt.IsZero() {
		t.Fatalf("delivery not updated: %+v", got)
	}

	if err := s.PutDelivery(domain.Delivery{ID: "bad"}); err == nil {
		t.Fatal("invalid delivery should be rejected")
	}
	bad := delivery
	bad.ID = "delivery-bad"
	bad.State = "merged"
	if err := s.PutDelivery(bad); err == nil {
		t.Fatal("unknown delivery state should be rejected")
	}
}
