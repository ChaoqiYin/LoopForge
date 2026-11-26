package workmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
)

// ProjectionStore is the part of repo.Store the Work Item projection needs.
// Keeping it narrow documents the boundary: the service may read records from
// the external platform, but it only writes Work Items through these calls.
type ProjectionStore interface {
	ManagementConnections() ([]domain.ManagementConnection, error)
	ManagementConnection(id string) (domain.ManagementConnection, bool)
	PutManagementConnection(conn domain.ManagementConnection) error
	ManagementFieldMappings(connectionID, tableID string) ([]domain.ManagementFieldMapping, error)
	ManagementStatusMappings(connectionID, tableID string) ([]domain.ManagementStatusMapping, error)
	PutManagementFieldMappings(items []domain.ManagementFieldMapping) error

	RecordWorkManagementEvent(event domain.WorkManagementEvent) (bool, error)
	UpdateWorkManagementEvent(event domain.WorkManagementEvent) error
	WorkManagementEventsDue(now time.Time, limit int) ([]domain.WorkManagementEvent, error)
	ResetWorkManagementEventsProcessing(cutoff time.Time) error

	IssueByExternalKey(connectionID, tableID, businessKey string) (domain.Issue, bool)
	IssueByExternalRecordID(connectionID, tableID, recordID string) (domain.Issue, bool)
	IssuesByExternalConnection(connectionID, tableID string) ([]domain.Issue, error)
	PutIssue(domain.Issue) error
	PatchIssue(id string, fn func(*domain.Issue) error) (domain.Issue, error)

	Project(id string) (domain.Project, bool)
	Projects() []domain.Project
	Repositories() []domain.Repository
}

// ServiceOptions tunes the event worker and reconciliation timers. Zero
// values pick production defaults; tests drive reconciliation explicitly.
type ServiceOptions struct {
	Now               func() time.Time
	Logger            func(format string, args ...any)
	ReconcileInterval time.Duration
	FullSyncInterval  time.Duration
	RetryBase         time.Duration
	RetryMax          time.Duration
	// AckTimeout bounds the synchronous Event Inbox write inside the Feishu
	// callback so the SDK acknowledges within three seconds.
	AckTimeout time.Duration
	// Execution receives status actions. Nil disables automatic actions.
	Execution RunExecution
}

// Service owns event intake, the Event Inbox worker, Work Item projection and
// reconciliation for the Management Adapter. Event acceptance only persists
// the inbox row; projection happens asynchronously so Feishu's three-second
// acknowledgement window is never blocked by external API calls.
type Service struct {
	adapter *FeishuAdapter
	store   ProjectionStore
	exec    RunExecution
	now     func() time.Time
	logger  func(format string, args ...any)

	reconcileEvery time.Duration
	fullSyncEvery  time.Duration
	retryBase      time.Duration
	retryMax       time.Duration
	ackTimeout     time.Duration

	mu sync.Mutex // serializes projection and reconciliation writes
	// syncMu serializes reconciliation passes so two runs cannot regress the
	// cursor or race their orphan detection.
	syncMu sync.Mutex
	// triggerMu serializes status action evaluation so an event and a
	// reconciliation pass cannot both dispatch the same Work Item.
	triggerMu sync.Mutex
	wake      chan struct{}

	startOnce sync.Once
}

// NewService builds the projection service. The caller normally calls Start to
// begin the inbox worker, reconciliation timers and event subscriptions.
func NewService(adapter *FeishuAdapter, store ProjectionStore, opts ServiceOptions) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Printf
	}
	reconcileEvery := opts.ReconcileInterval
	if reconcileEvery <= 0 {
		reconcileEvery = 15 * time.Minute
	}
	fullEvery := opts.FullSyncInterval
	if fullEvery <= 0 {
		fullEvery = 24 * time.Hour
	}
	retryBase := opts.RetryBase
	if retryBase <= 0 {
		retryBase = 5 * time.Second
	}
	retryMax := opts.RetryMax
	if retryMax <= 0 {
		retryMax = 10 * time.Minute
	}
	ackTimeout := opts.AckTimeout
	if ackTimeout <= 0 {
		ackTimeout = 2500 * time.Millisecond
	}
	exec := opts.Execution
	if exec == nil {
		exec = noopExecution{}
	}
	return &Service{
		adapter: adapter, store: store, exec: exec, now: now, logger: logger,
		reconcileEvery: reconcileEvery, fullSyncEvery: fullEvery,
		retryBase: retryBase, retryMax: retryMax, ackTimeout: ackTimeout,
		wake: make(chan struct{}, 1),
	}
}

// Start begins the inbox worker, recovers stranded rows, subscribes enabled
// connections to file events and schedules reconciliation. It is idempotent.
func (s *Service) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		cutoff := s.now().Add(-5 * time.Minute)
		if err := s.store.ResetWorkManagementEventsProcessing(cutoff); err != nil {
			s.logger("work management: reset stranded events: %v", err)
		}
		go s.worker(ctx)
		go s.ensureSubscriptions(ctx)
		go s.reconcileLoop(ctx)
	})
}

func (s *Service) ensureSubscriptions(ctx context.Context) {
	conns, err := s.store.ManagementConnections()
	if err != nil {
		s.logger("work management: list connections for subscription: %v", err)
		return
	}
	for _, conn := range conns {
		if !conn.Enabled || conn.Platform != domain.ManagementPlatformFeishuBitable {
			continue
		}
		_ = s.EnsureSubscription(ctx, conn)
	}
}

// EnsureSubscription subscribes the connection's Bitable file to record and
// field change events. A permission failure is visible through the returned
// error and marks the connection not-ready; callers decide whether that must
// fail their request.
func (s *Service) EnsureSubscription(ctx context.Context, conn domain.ManagementConnection) error {
	if !conn.Enabled || conn.Platform != domain.ManagementPlatformFeishuBitable {
		return nil
	}
	if err := s.adapter.SubscribeEvents(ctx, conn); err != nil {
		s.logger("work management: subscribe %s: %s", conn.ID, UserMessage(err))
		s.markNotReady(conn.ID, "事件订阅失败："+UserMessage(err))
		return err
	}
	return nil
}

func (s *Service) markNotReady(connID, message string) {
	conn, ok := s.store.ManagementConnection(connID)
	if !ok {
		return
	}
	conn.Readiness = domain.ManagementConnectionReadiness{
		State: domain.ManagementReadinessNotReady, Message: message, CheckedAt: s.now(),
	}
	if err := s.store.PutManagementConnection(conn); err != nil {
		s.logger("work management: persist readiness %s: %v", connID, err)
	}
}

func (s *Service) reconcileLoop(ctx context.Context) {
	timer := time.NewTimer(s.reconcileEvery)
	defer timer.Stop()
	fullAt := s.now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		conns, err := s.store.ManagementConnections()
		if err != nil {
			s.logger("work management: list connections for reconcile: %v", err)
			timer.Reset(s.reconcileEvery)
			continue
		}
		full := !s.now().Before(fullAt.Add(s.fullSyncEvery))
		for _, conn := range conns {
			if !conn.Enabled || conn.Platform != domain.ManagementPlatformFeishuBitable {
				continue
			}
			// Re-assert the per-file subscription so a connection enabled
			// after startup still receives events.
			_ = s.EnsureSubscription(ctx, conn)
			if _, err := s.SyncConnection(ctx, conn, full); err != nil {
				s.logger("work management: reconcile %s: %s", conn.ID, UserMessage(err))
			}
		}
		if full {
			fullAt = s.now()
		}
		timer.Reset(s.reconcileEvery)
	}
}

// ---------------------------------------------------------------------------
// Event intake
// ---------------------------------------------------------------------------

// RecordAction is one record change inside a Feishu record-changed event.
// Before and After are keyed by field id because field names are mutable.
type RecordAction struct {
	RecordID string        `json:"recordId"`
	Action   string        `json:"action"`
	Before   []ActionField `json:"before,omitempty"`
	After    []ActionField `json:"after,omitempty"`
}

// ActionField is one field id/value pair from an event body.
type ActionField struct {
	FieldID    string `json:"fieldId"`
	FieldValue string `json:"fieldValue,omitempty"`
}

// RecordChangedEvent is the platform-neutral shape of one record change
// delivery. One delivery may carry several record actions.
type RecordChangedEvent struct {
	EventID   string
	FileToken string
	TableID   string
	Actions   []RecordAction
}

// FieldChangedEvent announces that the table schema changed.
type FieldChangedEvent struct {
	EventID   string
	FileToken string
	TableID   string
}

// AcceptRecordChanged persists every action before returning, which is what
// lets the dispatcher acknowledge within Feishu's window. Projection runs
// later in the inbox worker.
func (s *Service) AcceptRecordChanged(ctx context.Context, event RecordChangedEvent) error {
	if strings.TrimSpace(event.EventID) == "" {
		return nil
	}
	now := s.now()
	if err := s.persistWithinAck(ctx, func() error {
		conn, ok := s.connectionFor(event.FileToken, event.TableID)
		if !ok {
			s.logger("work management: ignoring event %s for unconfigured file %s/%s", event.EventID, event.FileToken, event.TableID)
			return nil
		}
		for i, action := range event.Actions {
			inboxAction, ok := inboxActionFor(action.Action)
			if !ok {
				continue
			}
			payload, err := json.Marshal(action)
			if err != nil {
				return err
			}
			_, err = s.store.RecordWorkManagementEvent(domain.WorkManagementEvent{
				EventID:      event.EventID + ":" + strconv.Itoa(i) + ":" + strings.TrimSpace(action.RecordID) + ":" + string(inboxAction),
				ConnectionID: conn.ID, TableID: conn.TableID,
				RecordID: strings.TrimSpace(action.RecordID), Action: inboxAction,
				Payload: payload, Status: domain.ManagementEventPending, ReceivedAt: now,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.signal()
	return nil
}

// AcceptFieldChanged persists a schema change so the field id cache refresh
// happens off the acknowledgement path.
func (s *Service) AcceptFieldChanged(ctx context.Context, event FieldChangedEvent) error {
	if strings.TrimSpace(event.EventID) == "" {
		return nil
	}
	if err := s.persistWithinAck(ctx, func() error {
		conn, ok := s.connectionFor(event.FileToken, event.TableID)
		if !ok {
			return nil
		}
		_, err := s.store.RecordWorkManagementEvent(domain.WorkManagementEvent{
			EventID:      event.EventID + ":field_changed",
			ConnectionID: conn.ID, TableID: conn.TableID,
			Action:     domain.ManagementEventFieldChanged,
			Status:     domain.ManagementEventPending,
			ReceivedAt: s.now(),
		})
		return err
	}); err != nil {
		return err
	}
	s.signal()
	return nil
}

// errFeishuAckTimeout means the inbox write did not finish inside the
// acknowledgement budget. Returning an error makes Feishu redeliver; the
// redelivered event is deduplicated by event id if the write completes later.
var errFeishuAckTimeout = errors.New("飞书事件入库超时，等待平台重投")

func (s *Service) persistWithinAck(ctx context.Context, persist func() error) error {
	ackCtx, cancel := context.WithTimeout(ctx, s.ackTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- persist() }()
	select {
	case err := <-done:
		return err
	case <-ackCtx.Done():
		return errFeishuAckTimeout
	}
}

func inboxActionFor(action string) (domain.ManagementEventAction, bool) {
	switch strings.TrimSpace(action) {
	case string(domain.ManagementEventRecordAdded):
		return domain.ManagementEventRecordAdded, true
	case string(domain.ManagementEventRecordEdited):
		return domain.ManagementEventRecordEdited, true
	case string(domain.ManagementEventRecordDeleted):
		return domain.ManagementEventRecordDeleted, true
	}
	return "", false
}

func (s *Service) connectionFor(fileToken, tableID string) (domain.ManagementConnection, bool) {
	fileToken, tableID = strings.TrimSpace(fileToken), strings.TrimSpace(tableID)
	if fileToken == "" || tableID == "" {
		return domain.ManagementConnection{}, false
	}
	conns, err := s.store.ManagementConnections()
	if err != nil {
		s.logger("work management: list connections for event: %v", err)
		return domain.ManagementConnection{}, false
	}
	for _, conn := range conns {
		if conn.Platform != domain.ManagementPlatformFeishuBitable || !conn.Enabled {
			continue
		}
		if strings.TrimSpace(conn.BaseAppToken) == fileToken && strings.TrimSpace(conn.TableID) == tableID {
			return conn, true
		}
	}
	return domain.ManagementConnection{}, false
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(2 * time.Second):
		}
		for {
			events, err := s.store.WorkManagementEventsDue(s.now(), 50)
			if err != nil {
				s.logger("work management: read inbox: %v", err)
				break
			}
			if len(events) == 0 {
				break
			}
			for _, event := range events {
				if ctx.Err() != nil {
					return
				}
				s.processEvent(ctx, event)
			}
		}
	}
}

func (s *Service) processEvent(ctx context.Context, event domain.WorkManagementEvent) {
	event.Status = domain.ManagementEventProcessing
	event.Attempts++
	if err := s.store.UpdateWorkManagementEvent(event); err != nil {
		s.logger("work management: claim event %s: %v", event.EventID, err)
		return
	}
	err := s.handleEvent(ctx, event)
	now := s.now()
	if err != nil {
		event.Status = domain.ManagementEventFailed
		event.LastError = UserMessage(err)
		event.NextAttemptAt = now.Add(s.retryDelay(event.Attempts))
		event.ProcessedAt = time.Time{}
		s.logger("work management: event %s failed (attempt %d): %s", event.EventID, event.Attempts, event.LastError)
	} else {
		event.Status = domain.ManagementEventProcessed
		event.LastError = ""
		event.NextAttemptAt = time.Time{}
		event.ProcessedAt = now
	}
	if updateErr := s.store.UpdateWorkManagementEvent(event); updateErr != nil {
		s.logger("work management: finish event %s: %v", event.EventID, updateErr)
	}
}

func (s *Service) retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := s.retryBase
	for i := 1; i < attempts && delay < s.retryMax; i++ {
		delay *= 2
	}
	if delay > s.retryMax {
		return s.retryMax
	}
	return delay
}

func (s *Service) handleEvent(ctx context.Context, event domain.WorkManagementEvent) error {
	conn, ok := s.store.ManagementConnection(event.ConnectionID)
	if !ok || !conn.Enabled {
		return nil
	}
	switch event.Action {
	case domain.ManagementEventRecordAdded, domain.ManagementEventRecordEdited:
		record, err := s.adapter.ReadRecordForProjection(ctx, conn, event.RecordID)
		if err != nil {
			// The record may have been deleted between the event and the
			// read-back (formula fields force a read). Treat it like a
			// delete event instead of retrying an impossible read forever.
			if errors.Is(err, ErrRecordNotFound) {
				return s.orphanExternalRecord(conn, event.RecordID)
			}
			return err
		}
		return s.projectExternalRecord(ctx, conn, record)
	case domain.ManagementEventRecordDeleted:
		return s.orphanExternalRecord(conn, event.RecordID)
	case domain.ManagementEventFieldChanged:
		return s.refreshFieldMappings(ctx, conn)
	}
	return nil
}

func (s *Service) refreshFieldMappings(ctx context.Context, conn domain.ManagementConnection) error {
	resolved, err := s.adapter.ResolveFieldMappings(ctx, conn)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.PutManagementFieldMappings(resolved)
}

// ---------------------------------------------------------------------------
// Work Item projection
// ---------------------------------------------------------------------------

// projectExternalRecord upserts one external record into the issues table by
// (connection, table, business_key). Local execution fields are preserved;
// only the projection and the external business status snapshot change.
func (s *Service) projectExternalRecord(ctx context.Context, conn domain.ManagementConnection, record domain.ExternalRecord) error {
	businessKey := strings.TrimSpace(record.Ref.BusinessKey)
	if businessKey == "" {
		businessKey = strings.TrimSpace(record.Ref.RecordID)
		s.logger("work management: record %s in %s/%s has no business key; degraded to record id", businessKey, conn.ID, conn.TableID)
	}
	if businessKey == "" {
		return errors.New("外部记录缺少业务编号和记录 ID")
	}
	projectID := s.resolveProject(conn, record.ProjectID)

	s.mu.Lock()

	existing, found := s.store.IssueByExternalKey(conn.ID, conn.TableID, businessKey)
	before := snapshotStatus(existing)
	if !found {
		issue := domain.Issue{
			ID: workItemID(conn.ID, conn.TableID, businessKey), ProjectID: projectID,
			Title: strings.TrimSpace(record.Title), Status: "backlog", Kind: "requirement",
			Source: domain.ManagementPlatformFeishuBitable,
		}
		applyExternalRecord(&issue, conn, record, projectID, s.now(), s)
		issue.AppendOperation(domain.IssueOperation{At: s.now(), Actor: systemActor, Action: "external_sync", Detail: "从飞书多维表格同步"})
		if err := s.store.PutIssue(issue); err != nil {
			// A concurrent worker may have created the row already; fall back
			// to the identity lookup and update it.
			existing, found = s.store.IssueByExternalKey(conn.ID, conn.TableID, businessKey)
			if !found {
				s.mu.Unlock()
				return err
			}
			before = snapshotStatus(existing)
		} else {
			s.mu.Unlock()
			return s.afterProjection(ctx, conn, issue, before)
		}
	}
	updated, err := s.store.PatchIssue(existing.ID, func(issue *domain.Issue) error {
		applyExternalRecord(issue, conn, record, projectID, s.now(), s)
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if existing.ExternalRef != nil {
		oldRecord := strings.TrimSpace(existing.ExternalRef.RecordID)
		newRecord := firstNonEmpty(record.Ref.RecordID, businessKey)
		if oldRecord != "" && oldRecord != newRecord {
			// Collision policy: the business key is authoritative, so the
			// Work Item rebinds to its current record row. Duplicate business
			// keys in the table stay visible as an alert.
			s.logger("work management: business key %s rebinds record %s -> %s", businessKey, oldRecord, newRecord)
		}
	}
	return s.afterProjection(ctx, conn, updated, before)
}

func applyExternalRecord(issue *domain.Issue, conn domain.ManagementConnection, record domain.ExternalRecord, projectID string, now time.Time, service *Service) {
	issue.ProjectID = projectID
	issue.Title = strings.TrimSpace(record.Title)
	// Optional fields are only overwritten when the external record carries a
	// value; an unmapped field must not erase local execution context.
	if value := strings.TrimSpace(record.Description); value != "" {
		issue.Description = value
	}
	if value := strings.TrimSpace(record.Priority); value != "" {
		issue.Priority = value
	}
	if value := strings.TrimSpace(record.AcceptanceCriteria); value != "" {
		issue.AcceptanceCriteria = value
	}
	issue.ExternalAssignee = strings.TrimSpace(record.Assignee)
	if repositoryID, ok := service.resolveRepository(record.Repository, projectID, issue); ok {
		issue.RepositoryID = repositoryID
		if len(issue.RepositoryIDs) == 0 {
			issue.RepositoryIDs = []string{repositoryID}
		} else {
			found := false
			for _, id := range issue.RepositoryIDs {
				if id == repositoryID {
					found = true
				}
			}
			if !found {
				issue.RepositoryIDs = append([]string{repositoryID}, issue.RepositoryIDs...)
			}
		}
	}
	previousState := ""
	previousOrphaned := false
	var statusHistory []string
	writebackState := ""
	var writebackAt time.Time
	if issue.ExternalRef != nil {
		previousState = issue.ExternalRef.ExternalState
		previousOrphaned = issue.ExternalRef.Orphaned
		statusHistory = append([]string(nil), issue.ExternalRef.StatusHistory...)
		writebackState = issue.ExternalRef.WritebackState
		writebackAt = issue.ExternalRef.WritebackAt
	}
	ref := record.Ref
	ref.Source = domain.ManagementPlatformFeishuBitable
	ref.ExternalType = "record"
	ref.ConnectionID = conn.ID
	ref.TableID = conn.TableID
	ref.BusinessKey = firstNonEmpty(record.Ref.BusinessKey, record.Ref.RecordID)
	ref.RecordID = firstNonEmpty(record.Ref.RecordID, ref.BusinessKey)
	ref.ExternalID = firstNonEmpty(record.Ref.ExternalID, ref.RecordID)
	ref.ExternalURL = strings.TrimSpace(record.ExternalURL)
	ref.ExternalState = strings.TrimSpace(record.ExternalStatus)
	ref.Orphaned = false
	ref.OrphanedAt = time.Time{}
	ref.SyncedAt = now
	ref.WritebackState = writebackState
	ref.WritebackAt = writebackAt
	if previousState != ref.ExternalState && ref.ExternalState != "" {
		statusHistory = appendStatusHistory(statusHistory, ref.ExternalState)
	}
	ref.StatusHistory = statusHistory
	issue.ExternalRef = &ref
	issue.Source = domain.ManagementPlatformFeishuBitable
	if previousState != ref.ExternalState || previousOrphaned {
		issue.AppendOperation(domain.IssueOperation{
			At: ref.SyncedAt, Actor: systemActor, Action: "external_status",
			Detail: "外部业务状态：" + ref.ExternalState,
		})
	}
}

// resolveRepository maps the external repository value to a project
// repository by id or name. Unknown values are kept as-is only when they
// already reference a repository on the Work Item.
func (s *Service) resolveRepository(value, projectID string, issue *domain.Issue) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if issue != nil {
		for _, id := range issue.RepositoryIDs {
			if id == value {
				return value, true
			}
		}
		if issue.RepositoryID == value {
			return value, true
		}
	}
	for _, repository := range s.store.Repositories() {
		if repository.ProjectID != projectID {
			continue
		}
		if repository.ID == value || strings.EqualFold(strings.TrimSpace(repository.Name), value) {
			return repository.ID, true
		}
	}
	return "", false
}

// orphanExternalRecord marks a deleted external record. Execution evidence is
// kept: the Issue row, Runs and Traces are never deleted.
func (s *Service) orphanExternalRecord(conn domain.ManagementConnection, recordID string) error {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.store.IssueByExternalRecordID(conn.ID, conn.TableID, recordID)
	if !ok {
		// A record with a missing business key is projected under its record id.
		issue, ok = s.store.IssueByExternalKey(conn.ID, conn.TableID, recordID)
	}
	if !ok {
		return nil
	}
	if issue.ExternalRef != nil && issue.ExternalRef.Orphaned {
		return nil
	}
	_, err := s.store.PatchIssue(issue.ID, func(item *domain.Issue) error {
		if item.ExternalRef == nil {
			item.ExternalRef = &domain.ExternalRef{Source: domain.ManagementPlatformFeishuBitable, ConnectionID: conn.ID, TableID: conn.TableID}
		}
		item.ExternalRef.Orphaned = true
		item.ExternalRef.OrphanedAt = s.now()
		item.ExternalRef.SyncedAt = s.now()
		item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: "system", Action: "external_deleted", Detail: "外部记录已删除，保留执行证据"})
		return nil
	})
	return err
}

func (s *Service) resolveProject(conn domain.ManagementConnection, override string) string {
	override = strings.TrimSpace(override)
	if override == "" {
		return conn.DefaultProjectID
	}
	if _, ok := s.store.Project(override); ok {
		return override
	}
	for _, project := range s.store.Projects() {
		if strings.EqualFold(strings.TrimSpace(project.Key), override) {
			return project.ID
		}
	}
	s.logger("work management: record project %q not found; using connection default %q", override, conn.DefaultProjectID)
	return conn.DefaultProjectID
}

// ---------------------------------------------------------------------------
// Reconciliation
// ---------------------------------------------------------------------------

// SyncSummary reports one reconciliation pass.
type SyncSummary struct {
	Full      bool      `json:"full"`
	Scanned   int       `json:"scanned"`
	Projected int       `json:"projected"`
	Orphaned  int       `json:"orphaned"`
	Cursor    time.Time `json:"cursor,omitempty"`
}

// SyncNow runs one immediate reconciliation. It is incremental unless the
// connection has never completed a full sync.
func (s *Service) SyncNow(ctx context.Context, conn domain.ManagementConnection) (SyncSummary, error) {
	return s.SyncConnection(ctx, conn, conn.SyncCursor.FullSyncedAt.IsZero())
}

// SyncConnection pulls every remote page, projects records at or after the
// local watermark, and marks local Work Items missing from the remote listing
// as orphaned. The watermark is truncated to the start of its UTC day because
// Bitable date filters only reach whole days.
func (s *Service) SyncConnection(ctx context.Context, conn domain.ManagementConnection, full bool) (SyncSummary, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	mappings, err := s.store.ManagementFieldMappings(conn.ID, conn.TableID)
	if err != nil {
		return SyncSummary{}, err
	}
	if err := domain.ValidateManagementFieldMappings(mappings); err != nil {
		return SyncSummary{}, err
	}
	summary := SyncSummary{Full: full}
	startedAt := s.now()
	cutoff := conn.SyncCursor.LastModifiedAt
	if full || cutoff.IsZero() {
		cutoff = time.Time{}
	} else {
		cutoff = startOfUTCDay(cutoff)
	}
	seenKeys := map[string]bool{}
	seenRecords := map[string]bool{}
	cursor := ""
	maxModified := conn.SyncCursor.LastModifiedAt
	for {
		page, err := s.adapter.ListWorkItemsForProjection(ctx, conn, cursor, feishuReadPageSize)
		if err != nil {
			return summary, err
		}
		for _, record := range page.Records {
			summary.Scanned++
			key := firstNonEmpty(record.Ref.BusinessKey, record.Ref.RecordID)
			if key != "" {
				seenKeys[key] = true
			}
			if recordID := strings.TrimSpace(record.Ref.RecordID); recordID != "" {
				seenRecords[recordID] = true
			}
			if record.ModifiedAt.After(maxModified) {
				maxModified = record.ModifiedAt
			}
			if !cutoff.IsZero() && !record.ModifiedAt.IsZero() && record.ModifiedAt.Before(cutoff) {
				continue
			}
			if err := s.projectExternalRecord(ctx, conn, record); err != nil {
				return summary, err
			}
			summary.Projected++
		}
		if !page.HasMore || strings.TrimSpace(page.Cursor) == "" {
			break
		}
		cursor = page.Cursor
	}

	s.mu.Lock()
	local, err := s.store.IssuesByExternalConnection(conn.ID, conn.TableID)
	if err != nil {
		s.mu.Unlock()
		return summary, err
	}
	for _, issue := range local {
		if issue.ExternalRef == nil || issue.ExternalRef.Orphaned {
			continue
		}
		// A concurrent event may have projected this Work Item after the
		// remote snapshot was read; it is not missing remotely.
		if !issue.ExternalRef.SyncedAt.Before(startedAt) {
			continue
		}
		if seenKeys[issue.ExternalRef.BusinessKey] || seenRecords[issue.ExternalRef.RecordID] {
			continue
		}
		if _, err := s.store.PatchIssue(issue.ID, func(item *domain.Issue) error {
			if item.ExternalRef == nil {
				return nil
			}
			item.ExternalRef.Orphaned = true
			item.ExternalRef.OrphanedAt = s.now()
			item.AppendOperation(domain.IssueOperation{At: s.now(), Actor: "system", Action: "external_deleted", Detail: "对账未发现外部记录，标记为 orphaned"})
			return nil
		}); err != nil {
			s.mu.Unlock()
			return summary, err
		}
		summary.Orphaned++
	}
	s.mu.Unlock()

	conn.SyncCursor.LastModifiedAt = maxModified
	if full {
		conn.SyncCursor.FullSyncedAt = s.now()
	}
	summary.Cursor = maxModified
	if err := s.store.PutManagementConnection(conn); err != nil {
		return summary, err
	}
	return summary, nil
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func workItemID(connectionID, tableID, businessKey string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{connectionID, tableID, businessKey}, "\x00")))
	return "wi-" + hex.EncodeToString(sum[:12])
}
