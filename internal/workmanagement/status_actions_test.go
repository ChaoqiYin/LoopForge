package workmanagement

import (
	"testing"
	"time"

	"agentflow/internal/domain"
)

func TestFindStatusMappingMatchesTrimmedExternalStatus(t *testing.T) {
	mappings := []domain.ManagementStatusMapping{
		{ExternalStatus: "开发中", Action: domain.ManagementActionStartSOP, WorkflowID: "wf"},
		{ExternalStatus: "已完成", Action: domain.ManagementActionIgnore},
	}
	mapping, ok := findStatusMapping(mappings, " 开发中 ")
	if !ok || mapping.WorkflowID != "wf" {
		t.Fatalf("mapping = %+v ok=%v", mapping, ok)
	}
	if _, ok := findStatusMapping(mappings, "未知"); ok {
		t.Fatal("unknown status must not match")
	}
}

func TestStatusHistoryIsCappedAndDetectsRollback(t *testing.T) {
	var history []string
	for i := 0; i < 30; i++ {
		history = appendStatusHistory(history, "状态"+string(rune('A'+i%26)))
	}
	if len(history) > 20 {
		t.Fatalf("history not capped: %d", len(history))
	}
	if !statusSeen(appendStatusHistory(nil, "开发中"), "开发中") {
		t.Fatal("seen status was not detected")
	}
	if statusSeen(appendStatusHistory(nil, "开发中"), "评审中") {
		t.Fatal("unseen status was reported as a rollback")
	}
}

func TestSelfWritebackRequiresMarkerAndRecentTimestamp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	if !isSelfWriteback(statusSnapshot{writebackState: "已完成", writebackAt: now.Add(-time.Minute)}, "已完成", now) {
		t.Fatal("recent matching writeback was not recognized")
	}
	if isSelfWriteback(statusSnapshot{writebackState: "已完成", writebackAt: now.Add(-time.Hour)}, "已完成", now) {
		t.Fatal("stale writeback marker must not suppress a human change")
	}
	if isSelfWriteback(statusSnapshot{writebackState: "已完成", writebackAt: now}, "进行中", now) {
		t.Fatal("different status must not be treated as the writeback")
	}
	if isSelfWriteback(statusSnapshot{}, "已完成", now) {
		t.Fatal("missing marker must not suppress anything")
	}
}

func TestSnapshotStatusCopiesProjectionState(t *testing.T) {
	issue := domain.Issue{
		ExternalRef: &domain.ExternalRef{
			ExternalState: "开发中", StatusHistory: []string{"草稿"},
			WritebackState: "已完成", WritebackAt: time.Unix(1_700_000_000, 0).UTC(),
		},
		ExternalPendingAction: &domain.WorkItemPendingAction{Action: domain.ManagementActionStartSOP},
	}
	snapshot := snapshotStatus(issue)
	if snapshot.state != "开发中" || len(snapshot.history) != 1 || snapshot.writebackState != "已完成" || snapshot.pending == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshotStatus(domain.Issue{}).state != "" {
		t.Fatal("issue without an external ref must snapshot empty")
	}
}
