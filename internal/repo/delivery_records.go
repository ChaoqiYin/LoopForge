package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// Delivery records are independent of PR/MR providers. The provider URL is an
// optional display detail; the delivery state, commit and check results are
// LoopForge's own evidence.

type deliveryRow struct {
	ID           string     `gorm:"column:id;primaryKey;size:64"`
	WorkItemID   string     `gorm:"column:work_item_id;size:64;index:idx_deliveries_work_item"`
	ProjectID    string     `gorm:"column:project_id;size:64;index:idx_deliveries_project"`
	RepositoryID string     `gorm:"column:repository_id;size:64"`
	State        string     `gorm:"column:state;size:32;index:idx_deliveries_state"`
	Payload      []byte     `gorm:"column:payload;type:json"`
	Created      *time.Time `gorm:"column:created_at"`
	Updated      *time.Time `gorm:"column:updated_at"`
	Completed    *time.Time `gorm:"column:completed_at"`
}

func (deliveryRow) TableName() string { return "deliveries" }

func (row deliveryRow) toDomain() (domain.Delivery, error) {
	var delivery domain.Delivery
	if len(row.Payload) > 0 {
		if err := json.Unmarshal(row.Payload, &delivery); err != nil {
			return domain.Delivery{}, fmt.Errorf("读取交付记录失败")
		}
	}
	delivery.ID, delivery.WorkItemID, delivery.ProjectID = row.ID, row.WorkItemID, row.ProjectID
	delivery.RepositoryID, delivery.State = row.RepositoryID, row.State
	if row.Created != nil {
		delivery.CreatedAt = row.Created.UTC()
	}
	if row.Updated != nil {
		delivery.UpdatedAt = row.Updated.UTC()
	}
	delivery.CompletedAt = utcValue(row.Completed)
	return delivery, nil
}

func deliveryValues(delivery domain.Delivery, now time.Time) map[string]any {
	delivery.UpdatedAt = now
	return map[string]any{
		"id": delivery.ID, "work_item_id": delivery.WorkItemID, "project_id": delivery.ProjectID,
		"repository_id": delivery.RepositoryID, "state": delivery.State, "payload": mustJSON(delivery),
		"created_at": timePtr(delivery.CreatedAt), "updated_at": timePtr(now),
		"completed_at": timePtr(delivery.CompletedAt),
	}
}

// PutDelivery creates or replaces one Delivery record.
func (r *DeliveryRepo) PutDelivery(delivery domain.Delivery) error {
	if err := delivery.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if delivery.CreatedAt.IsZero() {
		if existing, ok := r.Delivery(delivery.ID); ok && !existing.CreatedAt.IsZero() {
			delivery.CreatedAt = existing.CreatedAt
		} else {
			delivery.CreatedAt = now
		}
	}
	return upsert(r.DB, "deliveries", deliveryValues(delivery, now))
}

// Delivery reads one Delivery record.
func (r *DeliveryRepo) Delivery(id string) (domain.Delivery, bool) {
	var row deliveryRow
	if err := r.DB.Where("id = ?", strings.TrimSpace(id)).Take(&row).Error; err != nil {
		return domain.Delivery{}, false
	}
	delivery, err := row.toDomain()
	if err != nil {
		return domain.Delivery{}, false
	}
	return delivery, true
}

// DeliveriesByWorkItem lists the delivery evidence of one Work Item, newest
// first.
func (r *DeliveryRepo) DeliveriesByWorkItem(workItemID string) ([]domain.Delivery, error) {
	var rows []deliveryRow
	err := r.DB.Where("work_item_id = ?", strings.TrimSpace(workItemID)).
		Order("created_at DESC, id DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.Delivery, 0, len(rows))
	for _, row := range rows {
		delivery, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, nil
}
