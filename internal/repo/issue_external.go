package repo

import (
	"strings"

	"agentflow/internal/domain"
)

// External Work Item projection lookups. A Formal Work Item is identified by
// (connection, table, business_key); the platform record id is only a pointer
// to the current row and is kept for deletion events.

func (r *IssuesRepo) issueByExternalColumn(column, connectionID, tableID, value string) (domain.Issue, bool) {
	connectionID, tableID, value = strings.TrimSpace(connectionID), strings.TrimSpace(tableID), strings.TrimSpace(value)
	if connectionID == "" || tableID == "" || value == "" {
		return domain.Issue{}, false
	}
	var row issueRow
	err := r.DB.Select("id").
		Where("connection_id = ? AND table_id = ? AND "+column+" = ?", connectionID, tableID, value).
		Take(&row).Error
	if err != nil {
		return domain.Issue{}, false
	}
	return r.Issue(row.ID)
}

// IssueByExternalKey finds the Work Item projected from one external record.
func (r *IssuesRepo) IssueByExternalKey(connectionID, tableID, businessKey string) (domain.Issue, bool) {
	return r.issueByExternalColumn("business_key", connectionID, tableID, businessKey)
}

// IssueByExternalRecordID finds the Work Item currently pointing at a platform
// record. Deletion events only carry the record id.
func (r *IssuesRepo) IssueByExternalRecordID(connectionID, tableID, recordID string) (domain.Issue, bool) {
	return r.issueByExternalColumn("external_id", connectionID, tableID, recordID)
}

// IssuesByExternalConnection lists every Work Item projected from one
// connection table. Reconciliation compares this set with the full remote
// listing to find records deleted without a delivered event.
func (r *IssuesRepo) IssuesByExternalConnection(connectionID, tableID string) ([]domain.Issue, error) {
	connectionID, tableID = strings.TrimSpace(connectionID), strings.TrimSpace(tableID)
	if connectionID == "" || tableID == "" {
		return nil, nil
	}
	var rows []issueRow
	if err := r.DB.Select("id").
		Where("connection_id = ? AND table_id = ?", connectionID, tableID).
		Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.Issue, 0, len(rows))
	for _, row := range rows {
		if issue, ok := r.Issue(row.ID); ok {
			out = append(out, issue)
		}
	}
	return out, nil
}
