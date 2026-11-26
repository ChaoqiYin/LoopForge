package workmanagement

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"agentflow/internal/domain"
)

type fakeAdapter struct{}

func (fakeAdapter) ValidateConnection(context.Context, domain.ManagementConnection) (domain.ManagementConnectionReadiness, error) {
	return domain.ManagementConnectionReadiness{State: domain.ManagementReadinessReady}, nil
}

func (fakeAdapter) GetWorkItem(context.Context, domain.ManagementConnection, domain.ExternalRef) (domain.ExternalRecord, error) {
	return domain.ExternalRecord{}, nil
}

func (fakeAdapter) ListWorkItems(context.Context, domain.ManagementConnection, string, int) (domain.ExternalRecordPage, error) {
	return domain.ExternalRecordPage{}, nil
}

func (fakeAdapter) UpdateExternalStatus(context.Context, domain.ManagementConnection, domain.ExternalRef, string) error {
	return nil
}

func (fakeAdapter) UpdateDeliverySummary(context.Context, domain.ManagementConnection, domain.ExternalRef, domain.ExternalDeliverySummary) error {
	return nil
}

func (fakeAdapter) SubscribeEvents(context.Context, domain.ManagementConnection) error {
	return nil
}

// var _ asserts the fake satisfies the boundary at compile time.
var _ Adapter = fakeAdapter{}

// TestAdapterBoundary keeps the interface to the six approved operations. The
// boundary explicitly excludes Business Approval and creating external records.
func TestAdapterBoundary(t *testing.T) {
	var _ Adapter = fakeAdapter{}
	typ := reflect.TypeOf((*Adapter)(nil)).Elem()
	got := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	want := []string{
		"GetWorkItem",
		"ListWorkItems",
		"SubscribeEvents",
		"UpdateDeliverySummary",
		"UpdateExternalStatus",
		"ValidateConnection",
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter method set = %v, want %v", got, want)
	}
}
