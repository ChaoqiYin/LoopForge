// Package workmanagement defines the Management Adapter boundary: the
// translation between one external work management platform and LoopForge's
// Work Items.
//
// The boundary deliberately excludes Business Approval and creating external
// records. Business Approval is owned by the external platform; LoopForge only
// observes its result as an External Business Status.
package workmanagement

import (
	"context"

	"agentflow/internal/domain"
)

// Adapter is implemented once per management platform. Feishu Bitable is the
// first implementation; tests drive it through a fake at the platform edge.
//
// The interface is the Service's dependency boundary: it covers the reads a
// projection/reconciliation pass needs, the two explicit write paths, the
// field-id cache refresh and event subscription. It deliberately excludes
// Business Approval and creating external records.
type Adapter interface {
	// ValidateConnection checks credentials, base/table access and field
	// visibility. A failed check returns a not-ready readiness, not a silent
	// degraded mode.
	ValidateConnection(ctx context.Context, conn domain.ManagementConnection) (domain.ManagementConnectionReadiness, error)
	// GetWorkItem reads one External Record by its stable reference.
	GetWorkItem(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef) (domain.ExternalRecord, error)
	// ListWorkItems reads one page of External Records for reconciliation.
	ListWorkItems(ctx context.Context, conn domain.ManagementConnection, cursor string, limit int) (domain.ExternalRecordPage, error)
	// ReadRecordForProjection reads one record leniently: a missing business
	// key or title degrades instead of aborting the projection.
	ReadRecordForProjection(ctx context.Context, conn domain.ManagementConnection, recordID string) (domain.ExternalRecord, error)
	// ListWorkItemsForProjection lists records leniently for reconciliation.
	ListWorkItemsForProjection(ctx context.Context, conn domain.ManagementConnection, cursor string, limit int) (domain.ExternalRecordPage, error)
	// ResolveFieldMappings refreshes the cached field id and type of each
	// configured mapping.
	ResolveFieldMappings(ctx context.Context, conn domain.ManagementConnection) ([]domain.ManagementFieldMapping, error)
	// UpdateExternalStatus writes a derived external business status back.
	UpdateExternalStatus(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef, status string) error
	// UpdateDeliverySummary writes the delivery summary fields back.
	UpdateDeliverySummary(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef, summary domain.ExternalDeliverySummary) error
	// SubscribeEvents subscribes the connection's file (base/table) to record
	// and field change events.
	SubscribeEvents(ctx context.Context, conn domain.ManagementConnection) error
}

var _ Adapter = (*FeishuAdapter)(nil)
