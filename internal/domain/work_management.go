package domain

import (
	"fmt"
	"strings"
	"time"
)

// This file holds the Work Management domain baseline: Management Connection,
// field and status mappings, the Event Inbox entry, Delivery and repository
// level Git service credentials. The types are deliberately platform-neutral:
// a Management Adapter translates one external platform into them.

// ManagementPlatformFeishuBitable is the first (and currently only) Management
// Adapter platform. The field is a plain string so a later adapter does not
// need a schema change.
const ManagementPlatformFeishuBitable = "feishu_bitable"

// Management Connection readiness states. A connection that fails self-check
// is "not_ready" and must not silently fall back to a degraded mode.
const (
	ManagementReadinessUnknown  = ""
	ManagementReadinessReady    = "ready"
	ManagementReadinessNotReady = "not_ready"
)

// WorkItemField names the semantic role of one external field in a Management
// Connection's field mapping. Only BusinessKey, Title and Status are required.
type WorkItemField string

const (
	WorkItemFieldBusinessKey        WorkItemField = "business_key"
	WorkItemFieldTitle              WorkItemField = "title"
	WorkItemFieldStatus             WorkItemField = "status"
	WorkItemFieldDescription        WorkItemField = "description"
	WorkItemFieldPriority           WorkItemField = "priority"
	WorkItemFieldAssignee           WorkItemField = "assignee"
	WorkItemFieldAcceptanceCriteria WorkItemField = "acceptance_criteria"
	WorkItemFieldRepository         WorkItemField = "repository"
	WorkItemFieldExternalURL        WorkItemField = "external_url"
	// WorkItemFieldProject optionally overrides the connection's default
	// Project per external record.
	WorkItemFieldProject WorkItemField = "project"
)

var workItemFields = []WorkItemField{
	WorkItemFieldBusinessKey, WorkItemFieldTitle, WorkItemFieldStatus,
	WorkItemFieldDescription, WorkItemFieldPriority, WorkItemFieldAssignee,
	WorkItemFieldAcceptanceCriteria, WorkItemFieldRepository, WorkItemFieldExternalURL,
	WorkItemFieldProject,
}

var requiredWorkItemFields = []WorkItemField{
	WorkItemFieldBusinessKey, WorkItemFieldTitle, WorkItemFieldStatus,
}

// Valid reports whether f is a known semantic field role.
func (f WorkItemField) Valid() bool {
	for _, known := range workItemFields {
		if f == known {
			return true
		}
	}
	return false
}

// Required reports whether a Management Connection must map this field.
func (f WorkItemField) Required() bool {
	for _, known := range requiredWorkItemFields {
		if f == known {
			return true
		}
	}
	return false
}

// RequiredWorkItemFields returns a copy of the mandatory field roles.
func RequiredWorkItemFields() []WorkItemField {
	return append([]WorkItemField(nil), requiredWorkItemFields...)
}

// ManagementFieldMapping maps one semantic Work Item field to an external
// field name. ExternalFieldID caches the platform field id; the configuration
// is edited by name because field ids are not user-facing.
type ManagementFieldMapping struct {
	ConnectionID      string        `json:"connectionId"`
	TableID           string        `json:"tableId"`
	Field             WorkItemField `json:"field"`
	ExternalFieldName string        `json:"externalFieldName"`
	ExternalFieldID   string        `json:"externalFieldId,omitempty"`
	UpdatedAt         time.Time     `json:"updatedAt,omitempty"`
}

// Validate checks one mapping row.
func (m ManagementFieldMapping) Validate() error {
	if strings.TrimSpace(m.ConnectionID) == "" || strings.TrimSpace(m.TableID) == "" ||
		!m.Field.Valid() || strings.TrimSpace(m.ExternalFieldName) == "" {
		return fmt.Errorf("字段映射无效")
	}
	return nil
}

// ValidateManagementFieldMappings checks a full connection table mapping:
// every row is valid, semantic and external names are unique, the table is
// consistent, and the mandatory fields are present.
func ValidateManagementFieldMappings(items []ManagementFieldMapping) error {
	if len(items) == 0 {
		return fmt.Errorf("字段映射不能为空")
	}
	connectionID, tableID := strings.TrimSpace(items[0].ConnectionID), strings.TrimSpace(items[0].TableID)
	seenField := map[WorkItemField]bool{}
	seenExternal := map[string]bool{}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(item.ConnectionID) != connectionID || strings.TrimSpace(item.TableID) != tableID {
			return fmt.Errorf("字段映射必须属于同一连接和表")
		}
		if seenField[item.Field] {
			return fmt.Errorf("字段映射重复：%s", item.Field)
		}
		seenField[item.Field] = true
		external := strings.TrimSpace(item.ExternalFieldName)
		if seenExternal[external] {
			return fmt.Errorf("外部字段重复：%s", external)
		}
		seenExternal[external] = true
	}
	for _, required := range requiredWorkItemFields {
		if !seenField[required] {
			return fmt.Errorf("缺少必填字段映射：%s", required)
		}
	}
	return nil
}

// ManagementStatusAction is the action triggered when an external business
// status changes. It never includes creating an external record or an approval
// entity.
type ManagementStatusAction string

const (
	// ManagementActionStartSOP dispatches the configured SOP.
	ManagementActionStartSOP ManagementStatusAction = "start_sop"
	// ManagementActionIgnore updates the projection without dispatching.
	ManagementActionIgnore ManagementStatusAction = "ignore"
	// ManagementActionPause requests the active run to pause.
	ManagementActionPause ManagementStatusAction = "pause"
	// ManagementActionCancel cancels the active run.
	ManagementActionCancel ManagementStatusAction = "cancel"
	// ManagementActionRestart cancels the active run and dispatches again.
	ManagementActionRestart ManagementStatusAction = "restart"
	// ManagementActionWriteback writes a derived status back to the external
	// platform without dispatching.
	ManagementActionWriteback ManagementStatusAction = "writeback"
)

// Valid reports whether a is a supported status action.
func (a ManagementStatusAction) Valid() bool {
	switch a {
	case ManagementActionStartSOP, ManagementActionIgnore, ManagementActionPause,
		ManagementActionCancel, ManagementActionRestart, ManagementActionWriteback:
		return true
	}
	return false
}

// WorkItemPendingAction is a status action that arrived while the Work Item
// already had an active Run. Only one action is kept: a newer status entry
// replaces the previous deferred one because the external state is
// authoritative.
type WorkItemPendingAction struct {
	Action          ManagementStatusAction `json:"action"`
	ExternalStatus  string                 `json:"externalStatus"`
	WorkflowID      string                 `json:"workflowId,omitempty"`
	WritebackStatus string                 `json:"writebackStatus,omitempty"`
	At              time.Time              `json:"at"`
}

// ManagementStatusMapping maps one external business status value to an
// action. The key is (connection, table, external_status).
type ManagementStatusMapping struct {
	ConnectionID    string                 `json:"connectionId"`
	TableID         string                 `json:"tableId"`
	ExternalStatus  string                 `json:"externalStatus"`
	Action          ManagementStatusAction `json:"action"`
	WorkflowID      string                 `json:"workflowId,omitempty"`
	WritebackStatus string                 `json:"writebackStatus,omitempty"`
	UpdatedAt       time.Time              `json:"updatedAt,omitempty"`
}

// Validate checks one status mapping row and the action's required fields.
func (m ManagementStatusMapping) Validate() error {
	if strings.TrimSpace(m.ConnectionID) == "" || strings.TrimSpace(m.TableID) == "" ||
		strings.TrimSpace(m.ExternalStatus) == "" {
		return fmt.Errorf("状态映射无效")
	}
	if !m.Action.Valid() {
		return fmt.Errorf("状态动作无效：%s", m.Action)
	}
	switch m.Action {
	case ManagementActionStartSOP:
		if strings.TrimSpace(m.WorkflowID) == "" {
			return fmt.Errorf("启动 SOP 必须指定工作流")
		}
	case ManagementActionWriteback:
		if strings.TrimSpace(m.WritebackStatus) == "" {
			return fmt.Errorf("回写必须指定外部状态")
		}
	}
	return nil
}

// ManagementSyncCursor is the connection's reconciliation watermark. The
// platform's date filters only reach whole days, so reconciliation truncates
// timestamps instead of trusting millisecond filtering.
type ManagementSyncCursor struct {
	LastModifiedAt time.Time `json:"lastModifiedAt,omitempty"`
	FullSyncedAt   time.Time `json:"fullSyncedAt,omitempty"`
	LastEventAt    time.Time `json:"lastEventAt,omitempty"`
}

// ManagementConnectionReadiness is the last self-check result.
type ManagementConnectionReadiness struct {
	State     string    `json:"state,omitempty"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

// ManagementConnection is the configured link to one external management
// platform instance: credentials reference, base/table, default Project,
// writeback switch, readiness and sync cursor. Field and status mappings are
// stored separately and keyed by the same (connection, table).
type ManagementConnection struct {
	ID               string                        `json:"id"`
	Name             string                        `json:"name"`
	Platform         string                        `json:"platform"`
	BotID            string                        `json:"botId,omitempty"`
	BaseAppToken     string                        `json:"baseAppToken"`
	TableID          string                        `json:"tableId"`
	DefaultProjectID string                        `json:"defaultProjectId"`
	AllowWriteback   bool                          `json:"allowWriteback"`
	Enabled          bool                          `json:"enabled"`
	Readiness        ManagementConnectionReadiness `json:"readiness,omitempty"`
	SyncCursor       ManagementSyncCursor          `json:"syncCursor,omitempty"`
	CreatedAt        time.Time                     `json:"createdAt"`
	UpdatedAt        time.Time                     `json:"updatedAt"`
}

// Validate checks the fields required to make a connection usable.
func (c ManagementConnection) Validate() error {
	switch {
	case strings.TrimSpace(c.ID) == "":
		return fmt.Errorf("连接 ID 不能为空")
	case strings.TrimSpace(c.Name) == "":
		return fmt.Errorf("连接名称不能为空")
	case strings.TrimSpace(c.Platform) == "":
		return fmt.Errorf("连接平台不能为空")
	case strings.TrimSpace(c.BaseAppToken) == "":
		return fmt.Errorf("连接必须配置 base app_token")
	case strings.TrimSpace(c.TableID) == "":
		return fmt.Errorf("连接必须配置数据表")
	case strings.TrimSpace(c.DefaultProjectID) == "":
		return fmt.Errorf("连接必须配置默认项目")
	}
	return nil
}

// ManagementEventAction is the external record change kind delivered to the
// Event Inbox.
type ManagementEventAction string

const (
	ManagementEventRecordAdded   ManagementEventAction = "record_added"
	ManagementEventRecordEdited  ManagementEventAction = "record_edited"
	ManagementEventRecordDeleted ManagementEventAction = "record_deleted"
	ManagementEventFieldChanged  ManagementEventAction = "field_changed"
)

// Valid reports whether a is a supported event action.
func (a ManagementEventAction) Valid() bool {
	switch a {
	case ManagementEventRecordAdded, ManagementEventRecordEdited, ManagementEventRecordDeleted, ManagementEventFieldChanged:
		return true
	}
	return false
}

// ManagementEventStatus is the Event Inbox processing state.
type ManagementEventStatus string

const (
	ManagementEventPending    ManagementEventStatus = "pending"
	ManagementEventProcessing ManagementEventStatus = "processing"
	ManagementEventProcessed  ManagementEventStatus = "processed"
	ManagementEventFailed     ManagementEventStatus = "failed"
	ManagementEventIgnored    ManagementEventStatus = "ignored"
)

// Valid reports whether s is a supported inbox state.
func (s ManagementEventStatus) Valid() bool {
	switch s {
	case ManagementEventPending, ManagementEventProcessing, ManagementEventProcessed, ManagementEventFailed, ManagementEventIgnored:
		return true
	}
	return false
}

// WorkManagementEvent is one Event Inbox row. EventID is the platform's
// unique delivery id: at-least-once delivery is deduplicated on it. Payload is
// the raw event body so processing can happen after the ack.
type WorkManagementEvent struct {
	ID            string                `json:"id"`
	EventID       string                `json:"eventId"`
	ConnectionID  string                `json:"connectionId"`
	TableID       string                `json:"tableId"`
	RecordID      string                `json:"recordId"`
	Action        ManagementEventAction `json:"action"`
	Payload       []byte                `json:"payload,omitempty"`
	Status        ManagementEventStatus `json:"status"`
	Attempts      int                   `json:"attempts"`
	LastError     string                `json:"lastError,omitempty"`
	ReceivedAt    time.Time             `json:"receivedAt,omitempty"`
	NextAttemptAt time.Time             `json:"nextAttemptAt,omitempty"`
	ProcessedAt   time.Time             `json:"processedAt,omitempty"`
	CreatedAt     time.Time             `json:"createdAt,omitempty"`
	UpdatedAt     time.Time             `json:"updatedAt,omitempty"`
}

// Validate checks the fields required to persist and process an event.
func (e WorkManagementEvent) Validate() error {
	switch {
	case strings.TrimSpace(e.EventID) == "":
		return fmt.Errorf("事件 ID 不能为空")
	case strings.TrimSpace(e.ConnectionID) == "":
		return fmt.Errorf("事件必须关联连接")
	case !e.Action.Valid():
		return fmt.Errorf("事件动作无效：%s", e.Action)
	case !e.Status.Valid():
		return fmt.Errorf("事件状态无效：%s", e.Status)
	}
	return nil
}

// Delivery check states. A check is optional until the SOP produces one.
const (
	DeliveryCheckPending = "pending"
	DeliveryCheckPassed  = "passed"
	DeliveryCheckFailed  = "failed"
	DeliveryCheckSkipped = "skipped"
)

// DeliveryState is the final delivery lifecycle state.
const (
	DeliveryStateDraft     = "draft"
	DeliveryStateRunning   = "running"
	DeliveryStateSucceeded = "succeeded"
	DeliveryStateFailed    = "failed"
	DeliveryStateCancelled = "cancelled"
)

// DeliveryCheck records one verification dimension (test, review, approval).
type DeliveryCheck struct {
	State     string    `json:"state,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	URL       string    `json:"url,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ValidDeliveryCheckState reports whether state is a supported check state.
func ValidDeliveryCheckState(state string) bool {
	switch state {
	case "", DeliveryCheckPending, DeliveryCheckPassed, DeliveryCheckFailed, DeliveryCheckSkipped:
		return true
	}
	return false
}

// Delivery is one development delivery produced by LoopForge. It does not
// depend on a PR/MR: ProviderMRURL is an optional provider detail.
type Delivery struct {
	ID             string        `json:"id"`
	WorkItemID     string        `json:"workItemId"`
	ProjectID      string        `json:"projectId"`
	RepositoryID   string        `json:"repositoryId"`
	RepositoryName string        `json:"repositoryName,omitempty"`
	Branch         string        `json:"branch,omitempty"`
	CommitSHA      string        `json:"commitSha,omitempty"`
	ProviderMRURL  string        `json:"providerMrUrl,omitempty"`
	Tests          DeliveryCheck `json:"tests,omitempty"`
	Review         DeliveryCheck `json:"review,omitempty"`
	Approval       DeliveryCheck `json:"approval,omitempty"`
	State          string        `json:"state"`
	CreatedAt      time.Time     `json:"createdAt,omitempty"`
	UpdatedAt      time.Time     `json:"updatedAt,omitempty"`
	CompletedAt    time.Time     `json:"completedAt,omitempty"`
}

// Validate checks the fields required to persist a delivery.
func (d Delivery) Validate() error {
	switch {
	case strings.TrimSpace(d.ID) == "":
		return fmt.Errorf("交付 ID 不能为空")
	case strings.TrimSpace(d.WorkItemID) == "":
		return fmt.Errorf("交付必须关联 Work Item")
	case strings.TrimSpace(d.ProjectID) == "":
		return fmt.Errorf("交付必须关联项目")
	}
	switch d.State {
	case DeliveryStateDraft, DeliveryStateRunning, DeliveryStateSucceeded, DeliveryStateFailed, DeliveryStateCancelled:
	default:
		return fmt.Errorf("交付状态无效：%s", d.State)
	}
	for _, check := range []DeliveryCheck{d.Tests, d.Review, d.Approval} {
		if !ValidDeliveryCheckState(check.State) {
			return fmt.Errorf("交付检查状态无效：%s", check.State)
		}
	}
	return nil
}

// Repository Git service credential kinds.
const (
	RepositoryGitCredentialSSH   = "ssh"
	RepositoryGitCredentialHTTPS = "https"
)

// RepositoryGitCredential is the safe metadata of a repository-level service
// credential. The secret is sealed with AES-GCM and never returned.
type RepositoryGitCredential struct {
	ID           string    `json:"id"`
	RepositoryID string    `json:"repositoryId"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Host         string    `json:"host"`
	Username     string    `json:"username,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

// RepositoryGitCredentialInput carries a new or replacement secret.
type RepositoryGitCredentialInput struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret"`
}

// Validate checks a service credential before it is sealed.
func (in RepositoryGitCredentialInput) Validate() error {
	name := strings.TrimSpace(in.Name)
	if count := len([]rune(name)); count < 1 || count > 80 {
		return fmt.Errorf("请输入 1 到 80 个字符的凭据名称")
	}
	switch in.Kind {
	case RepositoryGitCredentialSSH:
	case RepositoryGitCredentialHTTPS:
		if strings.TrimSpace(in.Username) == "" {
			return fmt.Errorf("HTTPS 凭据必须填写用户名")
		}
	default:
		return fmt.Errorf("请选择有效的仓库凭据类型")
	}
	if strings.TrimSpace(in.Host) == "" {
		return fmt.Errorf("必须填写 Git 主机")
	}
	if strings.TrimSpace(in.Secret) == "" {
		return fmt.Errorf("必须填写凭据内容")
	}
	return nil
}

// WorkItemEventType names the normalized internal event later tickets emit.
type WorkItemEventType string

const (
	WorkItemEventCreated           WorkItemEventType = "WorkItemCreated"
	WorkItemEventUpdated           WorkItemEventType = "WorkItemUpdated"
	WorkItemEventAssigned          WorkItemEventType = "WorkItemAssigned"
	WorkItemEventStatusChanged     WorkItemEventType = "WorkItemStatusChanged"
	WorkItemEventDeliveryCompleted WorkItemEventType = "DeliveryCompleted"
)

// WorkItemEvent is the normalized event contract between Work Management and
// the execution side. Approval results enter only as observed external status.
type WorkItemEvent struct {
	Type           WorkItemEventType `json:"type"`
	WorkItemID     string            `json:"workItemId"`
	ConnectionID   string            `json:"connectionId,omitempty"`
	ExternalStatus string            `json:"externalStatus,omitempty"`
	InternalStatus string            `json:"internalStatus,omitempty"`
	At             time.Time         `json:"at"`
}

// ExternalRecord is one authoritative record read from a management platform.
type ExternalRecord struct {
	Ref                ExternalRef `json:"ref"`
	Title              string      `json:"title"`
	Description        string      `json:"description,omitempty"`
	Priority           string      `json:"priority,omitempty"`
	Assignee           string      `json:"assignee,omitempty"`
	AcceptanceCriteria string      `json:"acceptanceCriteria,omitempty"`
	Repository         string      `json:"repository,omitempty"`
	// ProjectID optionally overrides the connection's default Project. It is
	// resolved by the projection, never written to the external platform.
	ProjectID      string    `json:"projectId,omitempty"`
	ExternalStatus string    `json:"externalStatus,omitempty"`
	ExternalURL    string    `json:"externalUrl,omitempty"`
	ModifiedAt     time.Time `json:"modifiedAt,omitempty"`
}

// ExternalRecordPage is one page of listed external records.
type ExternalRecordPage struct {
	Records []ExternalRecord `json:"records"`
	Cursor  string           `json:"cursor,omitempty"`
	HasMore bool             `json:"hasMore"`
}

// ExternalDeliverySummary is the delivery detail a Management Adapter may
// write back to the external platform.
type ExternalDeliverySummary struct {
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commitSha,omitempty"`
	Tests     string `json:"tests,omitempty"`
	Review    string `json:"review,omitempty"`
	URL       string `json:"url,omitempty"`
}

// FormalWorkItem reports whether the Issue is bound to an External Record.
// Only Formal Work Items enter formal dispatch and delivery statistics.
func (i Issue) FormalWorkItem() bool {
	return i.ExternalRef != nil && strings.TrimSpace(i.ExternalRef.Source) != ""
}

// ExternalBusinessStatus returns the read-only snapshot of the external
// business status. It is never the local execution status.
func (i Issue) ExternalBusinessStatus() string {
	if i.ExternalRef == nil {
		return ""
	}
	return strings.TrimSpace(i.ExternalRef.ExternalState)
}
