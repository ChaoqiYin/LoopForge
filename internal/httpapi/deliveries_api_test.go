package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"agentflow/internal/domain"
)

func TestDeliveriesAPIReadsWorkItemEvidence(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	issue := domain.Issue{ID: "issue-delivery-api", ProjectID: "platform", Title: "交付", Status: "todo", Kind: "requirement"}
	if err := f.s.PutIssue(issue); err != nil {
		t.Fatal(err)
	}
	delivery := domain.Delivery{
		ID: "delivery-api", WorkItemID: issue.ID, ProjectID: "platform",
		RepositoryID: "repo-api", RepositoryName: "api", Branch: "work/delivery",
		CommitSHA: "abc123", State: domain.DeliveryStateSucceeded,
		Tests:    domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: "全部通过"},
		Review:   domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: "审查通过"},
		Approval: domain.DeliveryCheck{State: domain.DeliveryCheckSkipped, Summary: "无需审批"},
	}
	if err := f.s.PutDelivery(delivery); err != nil {
		t.Fatal(err)
	}

	rec := cvBRequest(t, f, http.MethodGet, "/api/deliveries?workItemId="+issue.ID, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deliveries: %d %s", rec.Code, rec.Body.String())
	}
	var list []domain.Delivery
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("delivery list = %+v, %v", list, err)
	}
	if got := list[0]; got.CommitSHA != "abc123" || got.State != domain.DeliveryStateSucceeded || got.Tests.State != domain.DeliveryCheckPassed {
		t.Fatalf("delivery payload = %+v", got)
	}

	rec = cvBRequest(t, f, http.MethodGet, "/api/deliveries/delivery-api", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("get delivery: %d %s", rec.Code, rec.Body.String())
	}
	var one domain.Delivery
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || one.ID != delivery.ID {
		t.Fatalf("delivery = %+v, %v", one, err)
	}

	rec = cvBRequest(t, f, http.MethodGet, "/api/deliveries?projectId=platform", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list project deliveries: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != delivery.ID {
		t.Fatalf("project delivery list = %+v, %v", list, err)
	}

	rec = cvBRequest(t, f, http.MethodGet, "/api/deliveries/delivery-missing", nil, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing delivery: %d %s", rec.Code, rec.Body.String())
	}
	rec = cvBRequest(t, f, http.MethodGet, "/api/deliveries", nil, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unfiltered delivery list: %d %s", rec.Code, rec.Body.String())
	}
}
