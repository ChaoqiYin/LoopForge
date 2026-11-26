package domain

import (
	"strings"
	"testing"
	"time"
)

func TestValidateManagementFieldMappings(t *testing.T) {
	required := func(tableID string) []ManagementFieldMapping {
		return []ManagementFieldMapping{
			{ConnectionID: "conn-1", TableID: tableID, Field: WorkItemFieldBusinessKey, ExternalFieldName: "业务编号"},
			{ConnectionID: "conn-1", TableID: tableID, Field: WorkItemFieldTitle, ExternalFieldName: "标题"},
			{ConnectionID: "conn-1", TableID: tableID, Field: WorkItemFieldStatus, ExternalFieldName: "状态"},
			{ConnectionID: "conn-1", TableID: tableID, Field: WorkItemFieldRepository, ExternalFieldName: "仓库"},
		}
	}
	for _, tc := range []struct {
		name  string
		items []ManagementFieldMapping
		want  string
	}{
		{"all required fields", required("tbl-1"), ""},
		{"missing required fields", required("tbl-1")[1:], "缺少必填字段映射"},
		{"duplicate semantic field", append(required("tbl-1"), ManagementFieldMapping{ConnectionID: "conn-1", TableID: "tbl-1", Field: WorkItemFieldTitle, ExternalFieldName: "另一个标题"}), "字段映射重复"},
		{"duplicate external field", append(required("tbl-1"), ManagementFieldMapping{ConnectionID: "conn-1", TableID: "tbl-1", Field: WorkItemFieldPriority, ExternalFieldName: "标题"}), "外部字段重复"},
		{"unknown semantic field", append(required("tbl-1"), ManagementFieldMapping{ConnectionID: "conn-1", TableID: "tbl-1", Field: "story_points", ExternalFieldName: "故事点"}), "字段映射无效"},
		{"mixed tables", append(required("tbl-1"), ManagementFieldMapping{ConnectionID: "conn-1", TableID: "tbl-2", Field: WorkItemFieldPriority, ExternalFieldName: "优先级"}), "同一连接和表"},
		{"empty external name", append(required("tbl-1"), ManagementFieldMapping{ConnectionID: "conn-1", TableID: "tbl-1", Field: WorkItemFieldPriority, ExternalFieldName: "  "}), "字段映射无效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWorkManagementError(t, ValidateManagementFieldMappings(tc.items), tc.want)
		})
	}
}

func TestManagementStatusMappingValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ManagementStatusMapping
		want string
	}{
		{"start sop", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "开发中", Action: ManagementActionStartSOP, WorkflowID: "sop-1"}, ""},
		{"start sop without workflow", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "开发中", Action: ManagementActionStartSOP}, "启动 SOP 必须指定工作流"},
		{"ignore", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "草稿", Action: ManagementActionIgnore}, ""},
		{"pause", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "暂停", Action: ManagementActionPause}, ""},
		{"cancel", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "已取消", Action: ManagementActionCancel}, ""},
		{"restart", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "打回", Action: ManagementActionRestart}, ""},
		{"writeback", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "评审中", Action: ManagementActionWriteback, WritebackStatus: "评审中"}, ""},
		{"writeback without status", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "评审中", Action: ManagementActionWriteback}, "回写必须指定外部状态"},
		{"unknown action", ManagementStatusMapping{ConnectionID: "c", TableID: "t", ExternalStatus: "完成", Action: "archive"}, "状态动作无效"},
		{"missing external status", ManagementStatusMapping{ConnectionID: "c", TableID: "t", Action: ManagementActionIgnore}, "状态映射无效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWorkManagementError(t, tc.in.Validate(), tc.want)
		})
	}
}

func TestManagementConnectionValidate(t *testing.T) {
	valid := ManagementConnection{
		ID: "conn-1", Name: "飞书需求表", Platform: ManagementPlatformFeishuBitable,
		BotID: "bot-1", BaseAppToken: "bascn", TableID: "tbl-1", DefaultProjectID: "proj-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid connection: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   ManagementConnection
	}{
		{"missing id", ManagementConnection{Name: "n", Platform: ManagementPlatformFeishuBitable, BaseAppToken: "b", TableID: "t", DefaultProjectID: "p"}},
		{"missing platform", ManagementConnection{ID: "c", Name: "n", BaseAppToken: "b", TableID: "t", DefaultProjectID: "p"}},
		{"missing base token", ManagementConnection{ID: "c", Name: "n", Platform: ManagementPlatformFeishuBitable, TableID: "t", DefaultProjectID: "p"}},
		{"missing table", ManagementConnection{ID: "c", Name: "n", Platform: ManagementPlatformFeishuBitable, BaseAppToken: "b", DefaultProjectID: "p"}},
		{"missing project", ManagementConnection{ID: "c", Name: "n", Platform: ManagementPlatformFeishuBitable, BaseAppToken: "b", TableID: "t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}

func TestWorkManagementEventValidate(t *testing.T) {
	valid := WorkManagementEvent{
		EventID: "evt-1", ConnectionID: "conn-1", TableID: "tbl-1", RecordID: "rec-1",
		Action: ManagementEventRecordEdited, Status: ManagementEventPending,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   WorkManagementEvent
	}{
		{"missing event id", WorkManagementEvent{ConnectionID: "c", Action: ManagementEventRecordEdited, Status: ManagementEventPending}},
		{"missing connection", WorkManagementEvent{EventID: "e", Action: ManagementEventRecordEdited, Status: ManagementEventPending}},
		{"unknown action", WorkManagementEvent{EventID: "e", ConnectionID: "c", Action: "record_moved", Status: ManagementEventPending}},
		{"unknown status", WorkManagementEvent{EventID: "e", ConnectionID: "c", Action: ManagementEventRecordEdited, Status: "queued"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}

func TestDeliveryValidate(t *testing.T) {
	valid := Delivery{ID: "d-1", WorkItemID: "issue-1", ProjectID: "proj-1", State: DeliveryStateRunning}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid delivery: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   Delivery
	}{
		{"missing id", Delivery{WorkItemID: "i", ProjectID: "p", State: DeliveryStateRunning}},
		{"missing work item", Delivery{ID: "d", ProjectID: "p", State: DeliveryStateRunning}},
		{"missing project", Delivery{ID: "d", WorkItemID: "i", State: DeliveryStateRunning}},
		{"unknown state", Delivery{ID: "d", WorkItemID: "i", ProjectID: "p", State: "merged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
	if valid.CompletedAt.IsZero() {
		// CompletedAt is informational; delivered states do not require it.
	}
}

func TestRepositoryGitCredentialInputValidate(t *testing.T) {
	ssh := RepositoryGitCredentialInput{Name: "部署密钥", Kind: RepositoryGitCredentialSSH, Host: "git.example.com", Secret: "-----BEGIN KEY-----"}
	if err := ssh.Validate(); err != nil {
		t.Fatalf("valid ssh credential: %v", err)
	}
	https := RepositoryGitCredentialInput{Name: "服务账号", Kind: RepositoryGitCredentialHTTPS, Host: "git.example.com", Username: "svc", Secret: "token"}
	if err := https.Validate(); err != nil {
		t.Fatalf("valid https credential: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   RepositoryGitCredentialInput
	}{
		{"missing name", RepositoryGitCredentialInput{Kind: RepositoryGitCredentialSSH, Host: "h", Secret: "s"}},
		{"unknown kind", RepositoryGitCredentialInput{Name: "n", Kind: "pat", Host: "h", Secret: "s"}},
		{"missing host", RepositoryGitCredentialInput{Name: "n", Kind: RepositoryGitCredentialSSH, Secret: "s"}},
		{"missing secret", RepositoryGitCredentialInput{Name: "n", Kind: RepositoryGitCredentialSSH, Host: "h"}},
		{"https without username", RepositoryGitCredentialInput{Name: "n", Kind: RepositoryGitCredentialHTTPS, Host: "h", Secret: "s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}

func TestIssueFormalWorkItemAndExternalStatus(t *testing.T) {
	local := Issue{ID: "local-1", Status: "todo"}
	if local.FormalWorkItem() {
		t.Fatal("an issue without an external record must not be formal")
	}
	formal := Issue{ID: "wi-1", Status: "todo", ExternalRef: &ExternalRef{
		Source: "feishu_bitable", ExternalID: "rec-1", ExternalState: "开发中",
		ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-1",
	}}
	if !formal.FormalWorkItem() {
		t.Fatal("a bound external record is a formal work item")
	}
	if got := formal.ExternalBusinessStatus(); got != "开发中" {
		t.Fatalf("external business status = %q", got)
	}
	if got := local.ExternalBusinessStatus(); got != "" {
		t.Fatalf("local issue external status = %q", got)
	}
}

func TestExternalRefBusinessKeyAndOrphan(t *testing.T) {
	ref := ExternalRef{ConnectionID: "conn-1", TableID: "tbl-1", BusinessKey: "REQ-1", RecordID: "rec-1"}
	if ref.ConnectionID != "conn-1" || ref.TableID != "tbl-1" || ref.BusinessKey != "REQ-1" || ref.RecordID != "rec-1" {
		t.Fatalf("business key fields not addressable: %+v", ref)
	}
	ref.Orphaned = true
	ref.OrphanedAt = time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	if !ref.Orphaned || ref.OrphanedAt.IsZero() {
		t.Fatalf("orphan marker not addressable: %+v", ref)
	}
}

func TestRunOriginClassifiesTemporaryRuns(t *testing.T) {
	formal := Run{ID: "run-formal"}
	if formal.IsTemporary() || !formal.IsFormal() {
		t.Fatalf("empty origin must be formal: %+v", formal)
	}
	scratch := Run{ID: "run-scratch", Origin: RunOriginAssistantScratch}
	if !scratch.IsTemporary() || scratch.IsFormal() {
		t.Fatalf("assistant scratch run must be temporary: %+v", scratch)
	}
}

func assertWorkManagementError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want contains %q", err, want)
	}
}
