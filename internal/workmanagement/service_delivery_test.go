package workmanagement

import (
	"context"
	"testing"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

// deliveryOutboxFixture creates a configured store, a fake Feishu adapter with
// configured delivery fields, one formal Work Item and one Delivery. Only the
// Feishu OpenAPI is faked; the outbox, retry and audit paths are real.
func deliveryOutboxFixture(t *testing.T, maxRetries int) (*Service, *repo.Store, *fakeFeishu, domain.Delivery) {
	t.Helper()
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()}, func(o *AdapterOptions) {
		o.DeliveryFieldNames = map[string]string{"branch": "交付分支", "commit": "Commit SHA", "tests": "测试"}
		o.MaxRetries = maxRetries
	})
	conn := testConnection()
	conn.AllowWriteback = true
	if err := store.PutManagementConnection(conn); err != nil {
		t.Fatal(err)
	}
	issue := domain.Issue{
		ID: "wi-1", ProjectID: "platform", Title: "交付", Status: "todo", Kind: "requirement",
		Source: domain.ManagementPlatformFeishuBitable,
		ExternalRef: &domain.ExternalRef{
			Source: domain.ManagementPlatformFeishuBitable, ConnectionID: "conn-1", TableID: "tbl-1",
			BusinessKey: "REQ-1", RecordID: "rec-1", ExternalState: "开发中",
		},
	}
	if err := store.PutIssue(issue); err != nil {
		t.Fatal(err)
	}
	delivery := domain.Delivery{
		ID: "dlv-1", WorkItemID: "wi-1", ProjectID: "platform",
		Branch: "work/x", CommitSHA: "abc123", State: domain.DeliveryStateSucceeded,
		Tests: domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: "tests passed"},
	}
	if err := store.PutDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	svc := NewService(adapter, store, ServiceOptions{
		Now:       func() time.Time { return time.Now().UTC() },
		RetryBase: 50 * time.Millisecond,
		RetryMax:  time.Second,
	})
	return svc, store, fake, delivery
}

func TestQueueDeliveryWritebackWritesSummaryThroughOutbox(t *testing.T) {
	svc, _, fake, delivery := deliveryOutboxFixture(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	svc.QueueDeliveryWriteback(delivery)

	deadline := time.Now().Add(5 * time.Second)
	for fake.callCount("batch_update") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("delivery summary writeback never reached the fake Feishu OpenAPI")
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, _ := fake.lastRequest("batch_update")
	records, _ := req.Body["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("batch_update body = %+v", req.Body)
	}
	record, _ := records[0].(map[string]any)
	fields, _ := record["fields"].(map[string]any)
	if fields["交付分支"] != "work/x" || fields["Commit SHA"] != "abc123" || fields["测试"] != "tests passed" {
		t.Fatalf("delivery summary fields = %+v", fields)
	}
}

func TestDeliveryWritebackFailureRetriesWithoutBlocking(t *testing.T) {
	// MaxRetries=1 means two 500 responses exhaust the in-call retry, so the
	// Event Inbox records a failure and retries it later.
	svc, store, fake, delivery := deliveryOutboxFixture(t, 1)
	fake.push("batch_update",
		cannedResponse{Status: 500, Body: `{"code":500,"msg":"server error"}`},
		cannedResponse{Status: 500, Body: `{"code":500,"msg":"server error"}`},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	svc.QueueDeliveryWriteback(delivery)

	deadline := time.Now().Add(6 * time.Second)
	for {
		issue, ok := store.Issue("wi-1")
		if ok && operationPresent(issue, operationDeliveryWriteback) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery writeback was not retried: calls=%d", fake.callCount("batch_update"))
		}
		time.Sleep(25 * time.Millisecond)
	}
	if calls := fake.callCount("batch_update"); calls < 2 {
		t.Fatalf("transient failure was not retried: calls=%d", calls)
	}
}

func operationPresent(issue domain.Issue, action string) bool {
	for _, op := range issue.Operations {
		if op.Action == action {
			return true
		}
	}
	return false
}
