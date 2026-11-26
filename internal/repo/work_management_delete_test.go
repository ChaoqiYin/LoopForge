package repo

import (
	"testing"

	"agentflow/internal/domain"
)

func TestWorkManagementDeleteConnectionRemovesMappings(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	conn := domain.ManagementConnection{
		ID: "conn-del", Name: "待删除", Platform: domain.ManagementPlatformFeishuBitable,
		BotID: "bot-1", BaseAppToken: "bascn", TableID: "tbl-1", DefaultProjectID: "platform",
	}
	if err := s.PutManagementConnection(conn); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManagementFieldMappings([]domain.ManagementFieldMapping{
		{ConnectionID: "conn-del", TableID: "tbl-1", Field: domain.WorkItemFieldBusinessKey, ExternalFieldName: "业务编号"},
		{ConnectionID: "conn-del", TableID: "tbl-1", Field: domain.WorkItemFieldTitle, ExternalFieldName: "标题"},
		{ConnectionID: "conn-del", TableID: "tbl-1", Field: domain.WorkItemFieldStatus, ExternalFieldName: "状态"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManagementStatusMappings([]domain.ManagementStatusMapping{
		{ConnectionID: "conn-del", TableID: "tbl-1", ExternalStatus: "草稿", Action: domain.ManagementActionIgnore},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteManagementConnection("conn-del"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ManagementConnection("conn-del"); ok {
		t.Fatal("connection still present")
	}
	if fields, err := s.ManagementFieldMappings("conn-del", "tbl-1"); err != nil || len(fields) != 0 {
		t.Fatalf("field mappings = %+v, %v", fields, err)
	}
	if statuses, err := s.ManagementStatusMappings("conn-del", "tbl-1"); err != nil || len(statuses) != 0 {
		t.Fatalf("status mappings = %+v, %v", statuses, err)
	}
	if err := s.DeleteManagementConnection("conn-del"); err == nil {
		t.Fatal("deleting a missing connection should fail")
	}
}
