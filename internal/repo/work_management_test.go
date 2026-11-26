package repo

import (
	"strings"
	"testing"
	"time"

	"agentflow/internal/domain"
)

func TestWorkManagementConnectionAndMappings(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	conn := domain.ManagementConnection{
		ID: "conn-1", Name: "飞书需求表", Platform: domain.ManagementPlatformFeishuBitable,
		BotID: "bot-1", BaseAppToken: "bascn", TableID: "tbl-1", DefaultProjectID: "proj-1",
		Enabled: true,
	}
	if err := s.PutManagementConnection(conn); err != nil {
		t.Fatal(err)
	}
	got, ok := s.ManagementConnection("conn-1")
	if !ok || got.Name != conn.Name || got.BaseAppToken != conn.BaseAppToken || got.TableID != conn.TableID || !got.Enabled {
		t.Fatalf("connection round trip = %+v, ok=%v", got, ok)
	}
	if list, err := s.ManagementConnections(); err != nil || len(list) != 1 || list[0].ID != "conn-1" {
		t.Fatalf("connections = %+v, %v", list, err)
	}

	mappings := []domain.ManagementFieldMapping{
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldBusinessKey, ExternalFieldName: "业务编号", ExternalFieldID: "fld-1"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldTitle, ExternalFieldName: "标题", ExternalFieldID: "fld-2"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldStatus, ExternalFieldName: "状态", ExternalFieldID: "fld-3"},
	}
	if err := s.PutManagementFieldMappings(mappings); err != nil {
		t.Fatal(err)
	}
	stored, err := s.ManagementFieldMappings("conn-1", "tbl-1")
	if err != nil || len(stored) != 3 {
		t.Fatalf("field mappings = %+v, %v", stored, err)
	}
	// Replacing the same (connection, table, field) updates instead of duplicating.
	mappings[0].ExternalFieldName = "需求编号"
	mappings[0].ExternalFieldID = "fld-9"
	if err := s.PutManagementFieldMappings(mappings); err != nil {
		t.Fatal(err)
	}
	stored, err = s.ManagementFieldMappings("conn-1", "tbl-1")
	if err != nil || len(stored) != 3 {
		t.Fatalf("field mappings after replace = %+v, %v", stored, err)
	}
	if stored[0].ExternalFieldName != "需求编号" || stored[0].ExternalFieldID != "fld-9" {
		t.Fatalf("field mapping not replaced: %+v", stored[0])
	}
	// Invalid mapping sets are rejected before any write.
	invalid := []domain.ManagementFieldMapping{{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldTitle, ExternalFieldName: "标题"}}
	if err := s.PutManagementFieldMappings(invalid); err == nil {
		t.Fatal("invalid mapping set should be rejected")
	}

	statuses := []domain.ManagementStatusMapping{
		{ConnectionID: "conn-1", TableID: "tbl-1", ExternalStatus: "开发中", Action: domain.ManagementActionStartSOP, WorkflowID: "sop-1"},
		{ConnectionID: "conn-1", TableID: "tbl-1", ExternalStatus: "草稿", Action: domain.ManagementActionIgnore},
	}
	if err := s.PutManagementStatusMappings(statuses); err != nil {
		t.Fatal(err)
	}
	storedStatuses, err := s.ManagementStatusMappings("conn-1", "tbl-1")
	if err != nil || len(storedStatuses) != 2 {
		t.Fatalf("status mappings = %+v, %v", storedStatuses, err)
	}
	statuses[1].Action = domain.ManagementActionPause
	if err := s.PutManagementStatusMappings(statuses); err != nil {
		t.Fatal(err)
	}
	storedStatuses, _ = s.ManagementStatusMappings("conn-1", "tbl-1")
	for _, item := range storedStatuses {
		if item.ExternalStatus == "草稿" && item.Action != domain.ManagementActionPause {
			t.Fatalf("status mapping not replaced: %+v", item)
		}
	}
	if err := s.PutManagementStatusMappings([]domain.ManagementStatusMapping{{ConnectionID: "conn-1", TableID: "tbl-1", ExternalStatus: "x", Action: domain.ManagementActionStartSOP}}); err == nil {
		t.Fatal("status mapping without workflow should be rejected")
	}
}

func TestWorkManagementEventInboxDeduplicatesByEventID(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	now := time.Now().UTC().Truncate(time.Second)
	event := domain.WorkManagementEvent{
		EventID: "evt-1", ConnectionID: "conn-1", TableID: "tbl-1", RecordID: "rec-1",
		Action: domain.ManagementEventRecordEdited, Status: domain.ManagementEventPending,
		Payload: []byte(`{"recordId":"rec-1"}`), ReceivedAt: now,
	}
	created, err := s.RecordWorkManagementEvent(event)
	if err != nil || !created {
		t.Fatalf("first event: created=%v err=%v", created, err)
	}
	created, err = s.RecordWorkManagementEvent(event)
	if err != nil || created {
		t.Fatalf("duplicate event must be deduplicated: created=%v err=%v", created, err)
	}
	if list, err := s.WorkManagementEventsByStatus(domain.ManagementEventPending, 10); err != nil || len(list) != 1 {
		t.Fatalf("pending events = %+v, %v", list, err)
	}
	stored, ok := s.WorkManagementEventByEventID("evt-1")
	if !ok || stored.RecordID != "rec-1" || stored.Action != domain.ManagementEventRecordEdited {
		t.Fatalf("event round trip = %+v, ok=%v", stored, ok)
	}
	if strings.TrimSpace(stored.ID) == "" {
		t.Fatal("stored event needs a local id")
	}

	stored.Status = domain.ManagementEventProcessed
	stored.ProcessedAt = now.Add(time.Second)
	stored.Attempts = 1
	if err := s.UpdateWorkManagementEvent(stored); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.WorkManagementEventsByStatus(domain.ManagementEventPending, 10); len(list) != 0 {
		t.Fatalf("processed event still pending: %+v", list)
	}
	updated, _ := s.WorkManagementEventByEventID("evt-1")
	if updated.Status != domain.ManagementEventProcessed || updated.Attempts != 1 {
		t.Fatalf("event not updated: %+v", updated)
	}

	if _, err := s.RecordWorkManagementEvent(domain.WorkManagementEvent{EventID: "", ConnectionID: "c", Action: domain.ManagementEventRecordEdited, Status: domain.ManagementEventPending}); err == nil {
		t.Fatal("event without event id should be rejected")
	}
}
