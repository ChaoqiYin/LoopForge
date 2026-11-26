package repo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow/internal/domain"

	"gorm.io/gorm"
)

// WorkManagementRepo owns Management Connections, their field and status
// mappings, and the Event Inbox. Management Adapters read records through it;
// the execution side only consumes project projections and normalized events.
// The struct itself is declared in repo.go with the other domain repos.

type managementConnectionRow struct {
	ID               string     `gorm:"column:id;primaryKey;size:64"`
	Name             string     `gorm:"column:name;size:255"`
	Platform         string     `gorm:"column:platform;size:32;index"`
	Enabled          bool       `gorm:"column:enabled"`
	DefaultProjectID string     `gorm:"column:default_project_id;size:64;index"`
	Payload          []byte     `gorm:"column:payload;type:json"`
	Created          *time.Time `gorm:"column:created_at"`
	Updated          *time.Time `gorm:"column:updated_at"`
}

func (managementConnectionRow) TableName() string { return "management_connections" }

type managementFieldMappingRow struct {
	ID                string     `gorm:"column:id;primaryKey;size:191"`
	ConnectionID      string     `gorm:"column:connection_id;size:64;uniqueIndex:uq_management_field,priority:1"`
	TableID           string     `gorm:"column:table_id;size:128;uniqueIndex:uq_management_field,priority:2"`
	Field             string     `gorm:"column:field;size:64;uniqueIndex:uq_management_field,priority:3"`
	ExternalFieldName string     `gorm:"column:external_field_name;size:255"`
	ExternalFieldID   string     `gorm:"column:external_field_id;size:255"`
	ExternalFieldType string     `gorm:"column:external_field_type;size:32"`
	Created           *time.Time `gorm:"column:created_at"`
	Updated           *time.Time `gorm:"column:updated_at"`
}

func (managementFieldMappingRow) TableName() string { return "management_field_mappings" }

type managementStatusMappingRow struct {
	ID               string     `gorm:"column:id;primaryKey;size:191"`
	ConnectionID     string     `gorm:"column:connection_id;size:64;uniqueIndex:uq_management_status,priority:1"`
	TableID          string     `gorm:"column:table_id;size:128;uniqueIndex:uq_management_status,priority:2"`
	ExternalStatus   string     `gorm:"column:external_status;size:255;uniqueIndex:uq_management_status,priority:3"`
	Action           string     `gorm:"column:action;size:32"`
	WorkflowID       string     `gorm:"column:workflow_id;size:64"`
	WritebackStatus  string     `gorm:"column:writeback_status;size:255"`
	AwaitingApproval bool       `gorm:"column:awaiting_approval"`
	Created          *time.Time `gorm:"column:created_at"`
	Updated          *time.Time `gorm:"column:updated_at"`
}

func (managementStatusMappingRow) TableName() string { return "management_status_mappings" }

type workManagementEventRow struct {
	ID            string     `gorm:"column:id;primaryKey;size:64"`
	EventID       string     `gorm:"column:event_id;size:191;uniqueIndex:uq_work_management_event"`
	ConnectionID  string     `gorm:"column:connection_id;size:64;index"`
	TableID       string     `gorm:"column:table_id;size:128"`
	RecordID      string     `gorm:"column:record_id;size:191;index"`
	Action        string     `gorm:"column:action;size:32"`
	Status        string     `gorm:"column:status;size:32;index"`
	Attempts      int        `gorm:"column:attempts"`
	LastError     string     `gorm:"column:last_error;type:text"`
	Payload       []byte     `gorm:"column:payload;type:json"`
	ReceivedAt    *time.Time `gorm:"column:received_at"`
	NextAttemptAt *time.Time `gorm:"column:next_attempt_at"`
	ProcessedAt   *time.Time `gorm:"column:processed_at"`
	Created       *time.Time `gorm:"column:created_at"`
	Updated       *time.Time `gorm:"column:updated_at"`
}

func (workManagementEventRow) TableName() string { return "work_management_events" }

func (row managementConnectionRow) toDomain() (domain.ManagementConnection, error) {
	var conn domain.ManagementConnection
	if len(row.Payload) > 0 {
		if err := json.Unmarshal(row.Payload, &conn); err != nil {
			return domain.ManagementConnection{}, fmt.Errorf("读取 Management Connection 失败")
		}
	}
	conn.ID, conn.Name, conn.Platform, conn.Enabled, conn.DefaultProjectID = row.ID, row.Name, row.Platform, row.Enabled, row.DefaultProjectID
	if row.Created != nil {
		conn.CreatedAt = row.Created.UTC()
	}
	if row.Updated != nil {
		conn.UpdatedAt = row.Updated.UTC()
	}
	return conn, nil
}

func managementConnectionValues(conn domain.ManagementConnection, created, updated time.Time) map[string]any {
	conn.CreatedAt, conn.UpdatedAt = created, updated
	return map[string]any{
		"id": conn.ID, "name": conn.Name, "platform": conn.Platform, "enabled": conn.Enabled,
		"default_project_id": conn.DefaultProjectID, "payload": mustJSON(conn),
		"created_at": timePtr(created), "updated_at": timePtr(updated),
	}
}

func (row managementFieldMappingRow) toDomain() domain.ManagementFieldMapping {
	return domain.ManagementFieldMapping{
		ConnectionID: row.ConnectionID, TableID: row.TableID, Field: domain.WorkItemField(row.Field),
		ExternalFieldName: row.ExternalFieldName, ExternalFieldID: row.ExternalFieldID,
		ExternalFieldType: row.ExternalFieldType,
		UpdatedAt:         utcValue(row.Updated),
	}
}

func (row managementStatusMappingRow) toDomain() domain.ManagementStatusMapping {
	return domain.ManagementStatusMapping{
		ConnectionID: row.ConnectionID, TableID: row.TableID, ExternalStatus: row.ExternalStatus,
		Action: domain.ManagementStatusAction(row.Action), WorkflowID: row.WorkflowID,
		WritebackStatus: row.WritebackStatus, AwaitingApproval: row.AwaitingApproval,
		UpdatedAt: utcValue(row.Updated),
	}
}

func (row workManagementEventRow) toDomain() domain.WorkManagementEvent {
	return domain.WorkManagementEvent{
		ID: row.ID, EventID: row.EventID, ConnectionID: row.ConnectionID, TableID: row.TableID,
		RecordID: row.RecordID, Action: domain.ManagementEventAction(row.Action),
		Payload: row.Payload, Status: domain.ManagementEventStatus(row.Status), Attempts: row.Attempts,
		LastError: row.LastError, ReceivedAt: utcValue(row.ReceivedAt),
		NextAttemptAt: utcValue(row.NextAttemptAt), ProcessedAt: utcValue(row.ProcessedAt),
		CreatedAt: utcValue(row.Created), UpdatedAt: utcValue(row.Updated),
	}
}

func utcValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func managementMappingID(prefix, connectionID, tableID, key string) string {
	raw := strings.Join([]string{strings.TrimSpace(connectionID), strings.TrimSpace(tableID), strings.TrimSpace(key)}, "\x00")
	return prefix + "-" + hexSHA256(raw)[:40]
}

// PutManagementConnection creates or replaces one Management Connection.
func (r *WorkManagementRepo) PutManagementConnection(conn domain.ManagementConnection) error {
	if err := conn.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	created := now
	if existing, ok := r.ManagementConnection(conn.ID); ok && !existing.CreatedAt.IsZero() {
		created = existing.CreatedAt
	}
	return upsert(r.DB, "management_connections", managementConnectionValues(conn, created, now))
}

// ManagementConnection reads one connection by ID.
func (r *WorkManagementRepo) ManagementConnection(id string) (domain.ManagementConnection, bool) {
	var row managementConnectionRow
	if err := r.DB.Where("id = ?", strings.TrimSpace(id)).Take(&row).Error; err != nil {
		return domain.ManagementConnection{}, false
	}
	conn, err := row.toDomain()
	if err != nil {
		return domain.ManagementConnection{}, false
	}
	return conn, true
}

// ManagementConnections lists every configured connection.
func (r *WorkManagementRepo) ManagementConnections() ([]domain.ManagementConnection, error) {
	var rows []managementConnectionRow
	if err := r.DB.Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.ManagementConnection, 0, len(rows))
	for _, row := range rows {
		conn, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, conn)
	}
	return out, nil
}

// DeleteManagementConnection removes one connection together with its field
// and status mappings. Event inbox rows are kept as audit evidence.
func (r *WorkManagementRepo) DeleteManagementConnection(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("连接 ID 不能为空")
	}
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("connection_id = ?", id).Delete(&managementFieldMappingRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("connection_id = ?", id).Delete(&managementStatusMappingRow{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ?", id).Delete(&managementConnectionRow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// PutManagementFieldMappings replaces the field mapping of one (connection,
// table) after validating the whole set.
func (r *WorkManagementRepo) PutManagementFieldMappings(items []domain.ManagementFieldMapping) error {
	if err := domain.ValidateManagementFieldMappings(items); err != nil {
		return err
	}
	connectionID, tableID := strings.TrimSpace(items[0].ConnectionID), strings.TrimSpace(items[0].TableID)
	now := time.Now().UTC()
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("connection_id = ? AND table_id = ?", connectionID, tableID).Delete(&managementFieldMappingRow{}).Error; err != nil {
			return err
		}
		for _, item := range items {
			item.ConnectionID, item.TableID = connectionID, tableID
			item.UpdatedAt = now
			row := managementFieldMappingRow{
				ID:           managementMappingID("fm", connectionID, tableID, string(item.Field)),
				ConnectionID: item.ConnectionID, TableID: item.TableID, Field: string(item.Field),
				ExternalFieldName: strings.TrimSpace(item.ExternalFieldName), ExternalFieldID: strings.TrimSpace(item.ExternalFieldID),
				ExternalFieldType: strings.TrimSpace(item.ExternalFieldType),
				Created:           timePtr(now), Updated: timePtr(now),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ManagementFieldMappings reads a connection table's field mapping.
func (r *WorkManagementRepo) ManagementFieldMappings(connectionID, tableID string) ([]domain.ManagementFieldMapping, error) {
	var rows []managementFieldMappingRow
	err := r.DB.Where("connection_id = ? AND table_id = ?", strings.TrimSpace(connectionID), strings.TrimSpace(tableID)).
		Order("field").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.ManagementFieldMapping, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// PutManagementStatusMappings replaces the status mapping of one (connection,
// table) after validating every action.
func (r *WorkManagementRepo) PutManagementStatusMappings(items []domain.ManagementStatusMapping) error {
	if len(items) == 0 {
		return fmt.Errorf("状态映射不能为空")
	}
	connectionID, tableID := strings.TrimSpace(items[0].ConnectionID), strings.TrimSpace(items[0].TableID)
	seen := map[string]bool{}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(item.ConnectionID) != connectionID || strings.TrimSpace(item.TableID) != tableID {
			return fmt.Errorf("状态映射必须属于同一连接和表")
		}
		key := strings.TrimSpace(item.ExternalStatus)
		if seen[key] {
			return fmt.Errorf("状态映射重复：%s", key)
		}
		seen[key] = true
	}
	now := time.Now().UTC()
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("connection_id = ? AND table_id = ?", connectionID, tableID).Delete(&managementStatusMappingRow{}).Error; err != nil {
			return err
		}
		for _, item := range items {
			row := managementStatusMappingRow{
				ID:           managementMappingID("sm", connectionID, tableID, strings.TrimSpace(item.ExternalStatus)),
				ConnectionID: connectionID, TableID: tableID, ExternalStatus: strings.TrimSpace(item.ExternalStatus),
				Action: string(item.Action), WorkflowID: strings.TrimSpace(item.WorkflowID),
				WritebackStatus:  strings.TrimSpace(item.WritebackStatus),
				AwaitingApproval: item.AwaitingApproval,
				Created:          timePtr(now), Updated: timePtr(now),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ManagementStatusMappings reads a connection table's status mapping.
func (r *WorkManagementRepo) ManagementStatusMappings(connectionID, tableID string) ([]domain.ManagementStatusMapping, error) {
	var rows []managementStatusMappingRow
	err := r.DB.Where("connection_id = ? AND table_id = ?", strings.TrimSpace(connectionID), strings.TrimSpace(tableID)).
		Order("external_status").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.ManagementStatusMapping, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// RecordWorkManagementEvent inserts one Event Inbox row and reports whether it
// was new. event_id is unique so at-least-once delivery is idempotent.
func (r *WorkManagementRepo) RecordWorkManagementEvent(event domain.WorkManagementEvent) (bool, error) {
	if strings.TrimSpace(event.EventID) == "" {
		return false, fmt.Errorf("事件 ID 不能为空")
	}
	if event.Status == "" {
		event.Status = domain.ManagementEventPending
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}
	if err := event.Validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(event.ID) == "" {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return false, err
		}
		event.ID = "wme-" + hex.EncodeToString(raw)
	}
	now := time.Now().UTC()
	values := map[string]any{
		"id": event.ID, "event_id": event.EventID, "connection_id": event.ConnectionID,
		"table_id": event.TableID, "record_id": event.RecordID, "action": string(event.Action),
		"status": string(event.Status), "attempts": event.Attempts, "last_error": event.LastError,
		"payload": event.Payload, "received_at": timePtr(event.ReceivedAt),
		"next_attempt_at": timePtr(event.NextAttemptAt), "processed_at": timePtr(event.ProcessedAt),
		"created_at": timePtr(now), "updated_at": timePtr(now),
	}
	return insertIgnore(r.DB, "work_management_events", values)
}

// WorkManagementEventByEventID reads one inbox row by the platform event id.
func (r *WorkManagementRepo) WorkManagementEventByEventID(eventID string) (domain.WorkManagementEvent, bool) {
	var row workManagementEventRow
	if err := r.DB.Where("event_id = ?", strings.TrimSpace(eventID)).Take(&row).Error; err != nil {
		return domain.WorkManagementEvent{}, false
	}
	return row.toDomain(), true
}

// WorkManagementEventsByStatus lists inbox rows ready for a worker.
func (r *WorkManagementRepo) WorkManagementEventsByStatus(status domain.ManagementEventStatus, limit int) ([]domain.WorkManagementEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []workManagementEventRow
	if err := r.DB.Where("status = ?", string(status)).Order("received_at, id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.WorkManagementEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// WorkManagementEventsDue lists pending and failed inbox rows whose retry time
// has arrived. Events are processed in receive order; the caller serializes
// per record because it drains this queue in a single worker.
func (r *WorkManagementRepo) WorkManagementEventsDue(now time.Time, limit int) ([]domain.WorkManagementEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []workManagementEventRow
	err := r.DB.Where("status IN ?", []string{string(domain.ManagementEventPending), string(domain.ManagementEventFailed)}).
		Where("(next_attempt_at IS NULL OR next_attempt_at <= ?)", now.UTC()).
		Order("received_at, id").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkManagementEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// ResetWorkManagementEventsProcessing returns rows stranded in "processing"
// (a crash mid-event) to the queue. Events younger than cutoff are left alone
// so another live worker is not disturbed.
func (r *WorkManagementRepo) ResetWorkManagementEventsProcessing(cutoff time.Time) error {
	return r.DB.Model(&workManagementEventRow{}).
		Where("status = ? AND updated_at < ?", string(domain.ManagementEventProcessing), cutoff.UTC()).
		Updates(map[string]any{
			"status":          string(domain.ManagementEventPending),
			"next_attempt_at": nil,
			"updated_at":      timePtr(time.Now().UTC()),
		}).Error
}

// UpdateWorkManagementEvent writes back processing state. It never inserts.
func (r *WorkManagementRepo) UpdateWorkManagementEvent(event domain.WorkManagementEvent) error {
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("事件本地 ID 不能为空")
	}
	return r.DB.Model(&workManagementEventRow{}).Where("id = ?", event.ID).Updates(map[string]any{
		"status": string(event.Status), "attempts": event.Attempts, "last_error": event.LastError,
		"next_attempt_at": timePtr(event.NextAttemptAt), "processed_at": timePtr(event.ProcessedAt),
		"updated_at": timePtr(time.Now().UTC()),
	}).Error
}
