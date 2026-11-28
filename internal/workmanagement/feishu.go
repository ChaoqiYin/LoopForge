package workmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

// Feishu Bitable is the first Management Adapter. It translates tenant-token
// authenticated Bitable record reads and explicit batch writes into the
// platform-neutral domain. The only external seam tests need is a fake Feishu
// OpenAPI origin; no internal seam is introduced.
const (
	feishuTokenPath = "/open-apis/auth/v3/tenant_access_token/internal"
	// feishuReadPageSize is the Bitable search page limit (500 records/page).
	feishuReadPageSize = 500
	// feishuWritePageSize keeps one batch update within the documented limit.
	feishuWritePageSize = 1000
)

// Stable error kinds. Handlers map them to HTTP statuses and render
// UserMessage for humans; tests assert with errors.Is.
var (
	ErrCredentialsUnavailable         = errors.New("management_credentials_unavailable")
	ErrFieldMapping                   = errors.New("management_field_mapping_invalid")
	ErrFieldMissing                   = errors.New("management_field_missing")
	ErrBusinessKeyMissing             = errors.New("management_business_key_missing")
	ErrRecordNotFound                 = errors.New("management_record_not_found")
	ErrWritebackDisabled              = errors.New("management_writeback_disabled")
	ErrWritebackPaused                = errors.New("management_writeback_paused")
	ErrReadOnlyTable                  = errors.New("management_table_read_only")
	ErrDeliveryWritebackNotConfigured = errors.New("management_delivery_writeback_not_configured")
	ErrSubscribeUnsupported           = errors.New("management_subscribe_unsupported")
	ErrFeishuUnavailable              = errors.New("management_feishu_unavailable")
	ErrFeishuPermission               = errors.New("management_feishu_permission")
	ErrFeishuRateLimited              = errors.New("management_feishu_rate_limited")
	ErrFeishuRejected                 = errors.New("management_feishu_rejected")
)

// AdapterError carries a human-readable message alongside a stable sentinel.
type AdapterError struct {
	Kind    error
	Message string
}

func (e *AdapterError) Error() string { return e.Kind.Error() + ": " + e.Message }
func (e *AdapterError) Unwrap() error { return e.Kind }

func adapterError(kind error, format string, args ...any) error {
	return &AdapterError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// UserMessage returns the human-readable part of an adapter error.
func UserMessage(err error) string {
	var ae *AdapterError
	if errors.As(err, &ae) {
		return ae.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// MappingSource supplies the configured field and status mapping for a
// connection. The repo.Store satisfies it.
type MappingSource interface {
	ManagementFieldMappings(connectionID, tableID string) ([]domain.ManagementFieldMapping, error)
	ManagementStatusMappings(connectionID, tableID string) ([]domain.ManagementStatusMapping, error)
}

// CredentialSource resolves the existing encrypted notification-Bot Feishu
// application credential: botConfigID is notification_bots.id, and the remote
// app identity is NotificationBot.BotID.
type CredentialSource func(ctx context.Context, botConfigID string) (appID, appSecret string, err error)

// AdapterOptions configures one Feishu adapter instance.
type AdapterOptions struct {
	BaseURL    string
	HTTPClient *http.Client
	Mappings   MappingSource
	// Credentials is required in production; tests may leave it nil only when
	// they set it explicitly.
	Credentials CredentialSource
	// Now and Sleep make retry timing deterministic in tests.
	Now          func() time.Time
	Sleep        func(ctx context.Context, d time.Duration) error
	MaxRetries   int
	MaxRetryWait time.Duration
	// DeliveryFieldNames maps delivery summary roles (branch, commit, tests,
	// review, url) to explicitly configured external field names. Writeback
	// never guesses field names.
	DeliveryFieldNames map[string]string
}

// FeishuAdapter implements the Management Adapter boundary for Feishu Bitable.
type FeishuAdapter struct {
	baseURL        string
	httpClient     *http.Client
	mappings       MappingSource
	credentials    CredentialSource
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	maxRetries     int
	maxRetryWait   time.Duration
	deliveryFields map[string]string
	tokenMu        sync.Mutex
	tokens         map[string]cachedTenantToken
}

type cachedTenantToken struct {
	value   string
	expires time.Time
}

type feishuCredentials struct {
	appID     string
	appSecret string
}

type feishuField struct {
	id   string
	name string
	// fieldType is the platform field type (or UI type when the numeric type is
	// absent). Projection caches it so a field_changed event can detect a type
	// change and stop writing into a reshaped column.
	fieldType string
}

// NewFeishuAdapter builds an adapter with production defaults.
func NewFeishuAdapter(opts AdapterOptions) *FeishuAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = lark.FeishuBaseUrl
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}
	maxRetries := opts.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	maxWait := opts.MaxRetryWait
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}
	return &FeishuAdapter{
		baseURL:        baseURL,
		httpClient:     httpClient,
		mappings:       opts.Mappings,
		credentials:    opts.Credentials,
		now:            now,
		sleep:          sleep,
		maxRetries:     maxRetries,
		maxRetryWait:   maxWait,
		deliveryFields: opts.DeliveryFieldNames,
		tokens:         map[string]cachedTenantToken{},
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *FeishuAdapter) client(creds feishuCredentials) *lark.Client {
	return lark.NewClient(creds.appID, creds.appSecret,
		lark.WithOpenBaseUrl(a.baseURL),
		lark.WithHttpClient(a.httpClient),
		lark.WithEnableTokenCache(false),
		lark.WithLogLevel(larkcore.LogLevelError),
	)
}

// ---------------------------------------------------------------------------
// Connection self-check
// ---------------------------------------------------------------------------

// ValidateAndResolve checks credentials, table access and every mapped field,
// and returns the mappings with cached field ids. A failure is reported as a
// not-ready readiness plus a readable error; it never silently degrades.
func (a *FeishuAdapter) ValidateAndResolve(ctx context.Context, conn domain.ManagementConnection) (domain.ManagementConnectionReadiness, []domain.ManagementFieldMapping, error) {
	if _, err := a.credentialsFor(ctx, conn); err != nil {
		return a.notReady(err), nil, err
	}
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return a.notReady(err), nil, err
	}
	fields, err := a.listFields(ctx, conn)
	if err != nil {
		return a.notReady(err), nil, err
	}
	resolved, missing := resolveMappings(mappings, fields)
	if len(missing) > 0 {
		err := adapterError(ErrFieldMissing, "数据表缺少字段：%s", strings.Join(missing, "、"))
		return a.notReady(err), resolved, err
	}
	return domain.ManagementConnectionReadiness{
		State: domain.ManagementReadinessReady, Message: "连接自检通过", CheckedAt: a.now().UTC(),
	}, resolved, nil
}

// ValidateConnection is the Management Adapter boundary method.
func (a *FeishuAdapter) ValidateConnection(ctx context.Context, conn domain.ManagementConnection) (domain.ManagementConnectionReadiness, error) {
	readiness, _, err := a.ValidateAndResolve(ctx, conn)
	return readiness, err
}

// ResolveFieldMappings refreshes the field id cache for a connection table.
func (a *FeishuAdapter) ResolveFieldMappings(ctx context.Context, conn domain.ManagementConnection) ([]domain.ManagementFieldMapping, error) {
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return nil, err
	}
	fields, err := a.listFields(ctx, conn)
	if err != nil {
		return nil, err
	}
	resolved, missing := resolveMappings(mappings, fields)
	if len(missing) > 0 {
		return nil, adapterError(ErrFieldMissing, "数据表缺少字段：%s", strings.Join(missing, "、"))
	}
	return resolved, nil
}

func (a *FeishuAdapter) notReady(err error) domain.ManagementConnectionReadiness {
	return domain.ManagementConnectionReadiness{
		State: domain.ManagementReadinessNotReady, Message: UserMessage(err), CheckedAt: a.now().UTC(),
	}
}

func resolveMappings(mappings []domain.ManagementFieldMapping, fields []feishuField) ([]domain.ManagementFieldMapping, []string) {
	byName := map[string]feishuField{}
	for _, field := range fields {
		byName[strings.TrimSpace(field.name)] = feishuField{
			id: strings.TrimSpace(field.id), name: field.name, fieldType: strings.TrimSpace(field.fieldType),
		}
	}
	resolved := make([]domain.ManagementFieldMapping, 0, len(mappings))
	missing := make([]string, 0)
	for _, mapping := range mappings {
		name := strings.TrimSpace(mapping.ExternalFieldName)
		field, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		mapping.ExternalFieldID = field.id
		mapping.ExternalFieldType = field.fieldType
		resolved = append(resolved, mapping)
	}
	return resolved, missing
}

func (a *FeishuAdapter) fieldMappings(conn domain.ManagementConnection) ([]domain.ManagementFieldMapping, error) {
	if a.mappings == nil {
		return nil, adapterError(ErrFieldMapping, "字段映射来源未配置")
	}
	items, err := a.mappings.ManagementFieldMappings(conn.ID, conn.TableID)
	if err != nil {
		return nil, adapterError(ErrFieldMapping, "读取字段映射失败：%v", err)
	}
	if err := domain.ValidateManagementFieldMappings(items); err != nil {
		return nil, adapterError(ErrFieldMapping, "%s", err.Error())
	}
	return items, nil
}

func (a *FeishuAdapter) credentialsFor(ctx context.Context, conn domain.ManagementConnection) (feishuCredentials, error) {
	if strings.TrimSpace(conn.BotID) == "" {
		return feishuCredentials{}, adapterError(ErrCredentialsUnavailable, "连接未关联飞书应用，请先在通知机器人中配置飞书应用")
	}
	if a.credentials == nil {
		return feishuCredentials{}, adapterError(ErrCredentialsUnavailable, "飞书应用凭证来源未配置")
	}
	appID, secret, err := a.credentials(ctx, conn.BotID)
	if err != nil {
		return feishuCredentials{}, adapterError(ErrCredentialsUnavailable, "飞书应用凭证不可用：%v", err)
	}
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(secret) == "" {
		return feishuCredentials{}, adapterError(ErrCredentialsUnavailable, "飞书应用凭证不完整，请重新填写 App Secret")
	}
	return feishuCredentials{appID: appID, appSecret: secret}, nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// GetWorkItem reads one external record by its stable reference. RecordID is
// the current row pointer; BusinessKey is the authoritative identity and is
// used when no record id is known yet.
func (a *FeishuAdapter) GetWorkItem(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef) (domain.ExternalRecord, error) {
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return domain.ExternalRecord{}, err
	}
	recordID := strings.TrimSpace(ref.RecordID)
	if recordID != "" {
		records, err := a.batchGet(ctx, conn, []string{recordID})
		if err != nil {
			return domain.ExternalRecord{}, err
		}
		if len(records) == 0 {
			return domain.ExternalRecord{}, adapterError(ErrRecordNotFound, "外部记录不存在或不可访问：%s", recordID)
		}
		return a.normalizeRecord(conn, mappings, records[0], true)
	}
	businessKey := strings.TrimSpace(ref.BusinessKey)
	if businessKey == "" {
		return domain.ExternalRecord{}, adapterError(ErrBusinessKeyMissing, "记录缺少业务唯一键，无法定位外部记录")
	}
	statusField, ok := mappingFor(mappings, domain.WorkItemFieldBusinessKey)
	if !ok {
		return domain.ExternalRecord{}, adapterError(ErrFieldMapping, "缺少业务编号字段映射")
	}
	filter := &larkbitable.FilterInfo{
		Conjunction: larkcore.StringPtr("and"),
		Conditions: []*larkbitable.Condition{{
			FieldName: larkcore.StringPtr(strings.TrimSpace(statusField.ExternalFieldName)),
			Operator:  larkcore.StringPtr("is"),
			Value:     []string{businessKey},
		}},
	}
	resp, err := a.search(ctx, conn, &larkbitable.SearchAppTableRecordReqBody{Filter: filter, AutomaticFields: larkcore.BoolPtr(true)}, "", 2)
	if err != nil {
		return domain.ExternalRecord{}, err
	}
	if resp == nil || resp.Data == nil || len(resp.Data.Items) == 0 {
		return domain.ExternalRecord{}, adapterError(ErrRecordNotFound, "未找到业务编号为 %s 的记录", businessKey)
	}
	return a.normalizeRecord(conn, mappings, resp.Data.Items[0], true)
}

// ReadRecordForProjection reads one record for the Work Item projection.
// Unlike GetWorkItem it tolerates a missing business key or title: the
// projection degrades to the record id and raises an alert instead of
// rejecting the record outright.
func (a *FeishuAdapter) ReadRecordForProjection(ctx context.Context, conn domain.ManagementConnection, recordID string) (domain.ExternalRecord, error) {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return domain.ExternalRecord{}, adapterError(ErrRecordNotFound, "记录 ID 不能为空")
	}
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return domain.ExternalRecord{}, err
	}
	records, err := a.batchGet(ctx, conn, []string{recordID})
	if err != nil {
		return domain.ExternalRecord{}, err
	}
	if len(records) == 0 {
		return domain.ExternalRecord{}, adapterError(ErrRecordNotFound, "外部记录不存在或不可访问：%s", recordID)
	}
	return a.normalizeRecord(conn, mappings, records[0], false)
}

// ListWorkItems reads one page of external records. The cursor is the
// platform page token; reconciliation truncates by ModifiedAt locally because
// date filters only reach whole days.
func (a *FeishuAdapter) ListWorkItems(ctx context.Context, conn domain.ManagementConnection, cursor string, limit int) (domain.ExternalRecordPage, error) {
	return a.listWorkItems(ctx, conn, cursor, limit, true)
}

// ListWorkItemsForProjection lists records leniently so one record without a
// business key cannot abort reconciliation; the projection degrades that
// record to its record id and alerts.
func (a *FeishuAdapter) ListWorkItemsForProjection(ctx context.Context, conn domain.ManagementConnection, cursor string, limit int) (domain.ExternalRecordPage, error) {
	return a.listWorkItems(ctx, conn, cursor, limit, false)
}

func (a *FeishuAdapter) listWorkItems(ctx context.Context, conn domain.ManagementConnection, cursor string, limit int, requireBusinessKey bool) (domain.ExternalRecordPage, error) {
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return domain.ExternalRecordPage{}, err
	}
	if limit <= 0 || limit > feishuReadPageSize {
		limit = 100
	}
	resp, err := a.search(ctx, conn, nil, cursor, limit)
	if err != nil {
		return domain.ExternalRecordPage{}, err
	}
	page := domain.ExternalRecordPage{Records: []domain.ExternalRecord{}}
	if resp == nil || resp.Data == nil {
		return page, nil
	}
	if resp.Data.HasMore != nil {
		page.HasMore = *resp.Data.HasMore
	}
	if resp.Data.PageToken != nil {
		page.Cursor = *resp.Data.PageToken
	}
	for _, item := range resp.Data.Items {
		record, err := a.normalizeRecord(conn, mappings, item, requireBusinessKey)
		if err != nil {
			return domain.ExternalRecordPage{}, err
		}
		page.Records = append(page.Records, record)
	}
	return page, nil
}

func (a *FeishuAdapter) listFields(ctx context.Context, conn domain.ManagementConnection) ([]feishuField, error) {
	var out []feishuField
	pageToken := ""
	for {
		var resp *larkbitable.ListAppTableFieldResp
		err := a.call(ctx, conn, func(token string, creds feishuCredentials) (int, http.Header, int, string, error) {
			builder := larkbitable.NewListAppTableFieldReqBuilder().
				AppToken(conn.BaseAppToken).TableId(conn.TableID).PageSize(200)
			if pageToken != "" {
				builder.PageToken(pageToken)
			}
			r, err := a.client(creds).Bitable.V1.AppTableField.List(ctx, builder.Build(), larkcore.WithTenantAccessToken(token))
			if err != nil {
				return 0, nil, 0, "", err
			}
			resp = r
			return r.StatusCode, r.Header, r.Code, r.Msg, nil
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Data == nil {
			return out, nil
		}
		for _, item := range resp.Data.Items {
			if item == nil || item.FieldName == nil {
				continue
			}
			fieldType := ""
			if item.Type != nil {
				fieldType = strconv.Itoa(*item.Type)
			} else if item.UiType != nil {
				fieldType = strings.TrimSpace(*item.UiType)
			}
			out = append(out, feishuField{id: stringValue(item.FieldId), name: *item.FieldName, fieldType: fieldType})
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return out, nil
		}
		pageToken = *resp.Data.PageToken
	}
}

func (a *FeishuAdapter) batchGet(ctx context.Context, conn domain.ManagementConnection, ids []string) ([]*larkbitable.AppTableRecord, error) {
	body := &larkbitable.BatchGetAppTableRecordReqBody{RecordIds: ids, AutomaticFields: larkcore.BoolPtr(true)}
	var resp *larkbitable.BatchGetAppTableRecordResp
	err := a.call(ctx, conn, func(token string, creds feishuCredentials) (int, http.Header, int, string, error) {
		req := larkbitable.NewBatchGetAppTableRecordReqBuilder().
			AppToken(conn.BaseAppToken).TableId(conn.TableID).Body(body).Build()
		r, err := a.client(creds).Bitable.V1.AppTableRecord.BatchGet(ctx, req, larkcore.WithTenantAccessToken(token))
		if err != nil {
			return 0, nil, 0, "", err
		}
		resp = r
		return r.StatusCode, r.Header, r.Code, r.Msg, nil
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Data == nil {
		return nil, nil
	}
	return resp.Data.Records, nil
}

func (a *FeishuAdapter) search(ctx context.Context, conn domain.ManagementConnection, body *larkbitable.SearchAppTableRecordReqBody, pageToken string, pageSize int) (*larkbitable.SearchAppTableRecordResp, error) {
	if body == nil {
		body = &larkbitable.SearchAppTableRecordReqBody{}
	}
	if body.AutomaticFields == nil {
		body.AutomaticFields = larkcore.BoolPtr(true)
	}
	var resp *larkbitable.SearchAppTableRecordResp
	err := a.call(ctx, conn, func(token string, creds feishuCredentials) (int, http.Header, int, string, error) {
		builder := larkbitable.NewSearchAppTableRecordReqBuilder().
			AppToken(conn.BaseAppToken).TableId(conn.TableID).PageSize(pageSize).Body(body)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		r, err := a.client(creds).Bitable.V1.AppTableRecord.Search(ctx, builder.Build(), larkcore.WithTenantAccessToken(token))
		if err != nil {
			return 0, nil, 0, "", err
		}
		resp = r
		return r.StatusCode, r.Header, r.Code, r.Msg, nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *FeishuAdapter) normalizeRecord(conn domain.ManagementConnection, mappings []domain.ManagementFieldMapping, item *larkbitable.AppTableRecord, requireBusinessKey bool) (domain.ExternalRecord, error) {
	if item == nil {
		return domain.ExternalRecord{}, adapterError(ErrRecordNotFound, "外部记录不存在")
	}
	fields := item.Fields
	value := func(role domain.WorkItemField) (string, bool) {
		mapping, ok := mappingFor(mappings, role)
		if !ok {
			return "", false
		}
		return lookupField(fields, mapping)
	}
	recordID := stringValue(item.RecordId)
	businessKey, found := value(domain.WorkItemFieldBusinessKey)
	if !found || strings.TrimSpace(businessKey) == "" {
		name := strings.TrimSpace(fieldName(mappings, domain.WorkItemFieldBusinessKey))
		if requireBusinessKey {
			return domain.ExternalRecord{}, adapterError(ErrBusinessKeyMissing, "记录缺少业务编号字段：%s", name)
		}
		// Projection degrades to the record id and raises a visible alert. The
		// adapter keeps the missing value so that degradation stays observable
		// rather than looking like a legitimate business key.
		businessKey = ""
	}
	title, found := value(domain.WorkItemFieldTitle)
	if !found || strings.TrimSpace(title) == "" {
		name := strings.TrimSpace(fieldName(mappings, domain.WorkItemFieldTitle))
		if requireBusinessKey {
			return domain.ExternalRecord{}, adapterError(ErrFieldMissing, "记录缺少标题字段：%s", name)
		}
		title = recordID
	}
	status, _ := value(domain.WorkItemFieldStatus)
	description, _ := value(domain.WorkItemFieldDescription)
	priority, _ := value(domain.WorkItemFieldPriority)
	assignee, _ := value(domain.WorkItemFieldAssignee)
	acceptance, _ := value(domain.WorkItemFieldAcceptanceCriteria)
	repository, _ := value(domain.WorkItemFieldRepository)
	projectID, _ := value(domain.WorkItemFieldProject)
	externalURL := ""
	if mapping, ok := mappingFor(mappings, domain.WorkItemFieldExternalURL); ok {
		if raw, found := lookupRaw(fields, mapping); found {
			externalURL = feishuFieldURL(raw)
		}
	}
	if strings.TrimSpace(externalURL) == "" {
		externalURL = firstNonEmpty(stringValue(item.RecordUrl), stringValue(item.SharedUrl))
	}
	var modified time.Time
	if item.LastModifiedTime != nil && *item.LastModifiedTime > 0 {
		modified = time.UnixMilli(*item.LastModifiedTime).UTC()
	}
	ref := domain.ExternalRef{
		Source:        domain.ManagementPlatformFeishuBitable,
		ExternalID:    recordID,
		ExternalType:  "record",
		ExternalURL:   externalURL,
		ExternalState: strings.TrimSpace(status),
		ConnectionID:  conn.ID,
		TableID:       conn.TableID,
		BusinessKey:   strings.TrimSpace(businessKey),
		RecordID:      recordID,
		SyncedAt:      a.now().UTC(),
	}
	return domain.ExternalRecord{
		Ref:                ref,
		Title:              strings.TrimSpace(title),
		Description:        description,
		Priority:           priority,
		Assignee:           assignee,
		AcceptanceCriteria: acceptance,
		Repository:         repository,
		ProjectID:          strings.TrimSpace(projectID),
		ExternalStatus:     strings.TrimSpace(status),
		ExternalURL:        externalURL,
		ModifiedAt:         modified,
	}, nil
}

// ResolveStatusMapping returns the configured action for one external status.
// An unknown status is reported as not known, so the caller only projects it
// and never triggers execution.
func (a *FeishuAdapter) ResolveStatusMapping(ctx context.Context, conn domain.ManagementConnection, externalStatus string) (domain.ManagementStatusMapping, bool, error) {
	status := strings.TrimSpace(externalStatus)
	if status == "" {
		return domain.ManagementStatusMapping{}, false, nil
	}
	if a.mappings == nil {
		return domain.ManagementStatusMapping{}, false, adapterError(ErrFieldMapping, "状态映射来源未配置")
	}
	items, err := a.mappings.ManagementStatusMappings(conn.ID, conn.TableID)
	if err != nil {
		return domain.ManagementStatusMapping{}, false, adapterError(ErrFieldMapping, "读取状态映射失败：%v", err)
	}
	for _, item := range items {
		if strings.TrimSpace(item.ExternalStatus) == status {
			return item, true, nil
		}
	}
	return domain.ManagementStatusMapping{}, false, nil
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// UpdateExternalStatus writes one derived external status. It only writes the
// mapped status field and refuses when the connection has writeback disabled.
func (a *FeishuAdapter) UpdateExternalStatus(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef, status string) error {
	if !conn.AllowWriteback {
		return adapterError(ErrWritebackDisabled, "该连接未开启回写，外部状态保持不变")
	}
	if conn.WritebackPaused {
		return adapterError(ErrWritebackPaused, "外部字段结构已变更，自动回写已暂停：%s", strings.TrimSpace(conn.WritebackPausedReason))
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return adapterError(ErrFeishuRejected, "回写状态不能为空")
	}
	mappings, err := a.fieldMappings(conn)
	if err != nil {
		return err
	}
	statusMapping, ok := mappingFor(mappings, domain.WorkItemFieldStatus)
	if !ok {
		return adapterError(ErrFieldMapping, "缺少状态字段映射")
	}
	recordID, err := a.recordID(ctx, conn, ref)
	if err != nil {
		return err
	}
	return a.batchUpdateFields(ctx, conn, recordID, map[string]any{
		strings.TrimSpace(statusMapping.ExternalFieldName): status,
	})
}

// UpdateDeliverySummary writes only explicitly configured delivery fields.
func (a *FeishuAdapter) UpdateDeliverySummary(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef, summary domain.ExternalDeliverySummary) error {
	if !conn.AllowWriteback {
		return adapterError(ErrWritebackDisabled, "该连接未开启回写，交付摘要保持不变")
	}
	if conn.WritebackPaused {
		return adapterError(ErrWritebackPaused, "外部字段结构已变更，自动回写已暂停：%s", strings.TrimSpace(conn.WritebackPausedReason))
	}
	if len(a.deliveryFields) == 0 {
		return adapterError(ErrDeliveryWritebackNotConfigured, "未配置交付摘要字段映射，暂不回写交付信息")
	}
	values := map[string]string{
		"branch": summary.Branch,
		"commit": summary.CommitSHA,
		"tests":  summary.Tests,
		"review": summary.Review,
		"url":    summary.URL,
	}
	fields := map[string]any{}
	for role, value := range values {
		name := strings.TrimSpace(a.deliveryFields[role])
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		fields[name] = value
	}
	if len(fields) == 0 {
		return nil
	}
	recordID, err := a.recordID(ctx, conn, ref)
	if err != nil {
		return err
	}
	return a.batchUpdateFields(ctx, conn, recordID, fields)
}

// SubscribeEvents subscribes the connection's Bitable file to record and field
// change events. Feishu requires the application to be the document owner or
// an administrator; a permission failure is surfaced so the connection can be
// marked not-ready instead of silently missing events.
func (a *FeishuAdapter) SubscribeEvents(ctx context.Context, conn domain.ManagementConnection) error {
	if strings.TrimSpace(conn.BaseAppToken) == "" {
		return adapterError(ErrSubscribeUnsupported, "连接缺少 base app_token，无法订阅事件")
	}
	var resp *larkdrive.SubscribeFileResp
	err := a.call(ctx, conn, func(token string, creds feishuCredentials) (int, http.Header, int, string, error) {
		req := larkdrive.NewSubscribeFileReqBuilder().
			FileToken(conn.BaseAppToken).
			FileType(larkdrive.FileTypeSubscribeFileBitable).
			Build()
		r, err := a.client(creds).Drive.V1.File.Subscribe(ctx, req, larkcore.WithTenantAccessToken(token))
		if err != nil {
			return 0, nil, 0, "", err
		}
		resp = r
		return r.StatusCode, r.Header, r.Code, r.Msg, nil
	})
	if err != nil {
		return err
	}
	if resp == nil {
		return nil
	}
	return nil
}

func (a *FeishuAdapter) recordID(ctx context.Context, conn domain.ManagementConnection, ref domain.ExternalRef) (string, error) {
	if id := strings.TrimSpace(ref.RecordID); id != "" {
		return id, nil
	}
	record, err := a.GetWorkItem(ctx, conn, ref)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(record.Ref.RecordID) == "" {
		return "", adapterError(ErrRecordNotFound, "无法定位外部记录")
	}
	return record.Ref.RecordID, nil
}

func (a *FeishuAdapter) batchUpdateFields(ctx context.Context, conn domain.ManagementConnection, recordID string, fields map[string]any) error {
	if len(fields) > feishuWritePageSize {
		return adapterError(ErrFeishuRejected, "单次回写字段过多")
	}
	body := &larkbitable.BatchUpdateAppTableRecordReqBody{Records: []*larkbitable.AppTableRecord{{
		RecordId: larkcore.StringPtr(recordID),
		Fields:   fields,
	}}}
	return a.call(ctx, conn, func(token string, creds feishuCredentials) (int, http.Header, int, string, error) {
		req := larkbitable.NewBatchUpdateAppTableRecordReqBuilder().
			AppToken(conn.BaseAppToken).TableId(conn.TableID).Body(body).Build()
		resp, err := a.client(creds).Bitable.V1.AppTableRecord.BatchUpdate(ctx, req, larkcore.WithTenantAccessToken(token))
		if err != nil {
			return 0, nil, 0, "", err
		}
		return resp.StatusCode, resp.Header, resp.Code, resp.Msg, nil
	})
}

// ---------------------------------------------------------------------------
// Transport, token cache and retry
// ---------------------------------------------------------------------------

type feishuResult struct {
	status int
	header http.Header
	code   int
	msg    string
	err    error
}

func (r feishuResult) success() bool {
	return r.err == nil && r.status >= 200 && r.status < 300 && r.code == 0
}

func (r feishuResult) retryable() bool {
	if r.err != nil {
		return !errors.Is(r.err, context.Canceled) && !errors.Is(r.err, context.DeadlineExceeded)
	}
	return r.status == http.StatusTooManyRequests || r.status >= 500
}

func (r feishuResult) tokenInvalid() bool {
	if r.status == http.StatusUnauthorized {
		return true
	}
	switch r.code {
	case 99991661, 99991663, 99991668:
		return true
	}
	return false
}

func (r feishuResult) userError() error {
	if r.err != nil {
		return adapterError(ErrFeishuUnavailable, "飞书开放平台暂不可用：%v", r.err)
	}
	msg := strings.TrimSpace(r.msg)
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", r.status)
	}
	if isReadOnlyTableError(r.code, msg) {
		return adapterError(ErrReadOnlyTable, "该数据表来自其他数据源同步，只读不可写：%s", msg)
	}
	if isPermissionError(r.status, r.code, msg) {
		return adapterError(ErrFeishuPermission, "飞书拒绝了访问，请确认应用已被添加为多维表格的可管理者：%s", msg)
	}
	switch {
	case r.status == http.StatusTooManyRequests || r.code == 99991400:
		return adapterError(ErrFeishuRateLimited, "飞书接口触发限流，重试后仍失败：%s", msg)
	case r.status >= 500:
		return adapterError(ErrFeishuUnavailable, "飞书开放平台暂不可用（HTTP %d）：%s", r.status, msg)
	case r.status == http.StatusNotFound:
		return adapterError(ErrRecordNotFound, "飞书接口未找到目标：%s", msg)
	}
	return adapterError(ErrFeishuRejected, "飞书接口返回错误（HTTP %d，code %d）：%s", r.status, r.code, msg)
}

// call runs one authenticated Bitable operation. It refreshes an expired
// tenant token, retries 429/5xx and network failures, and rotates the token
// once when Feishu reports it invalid.
func (a *FeishuAdapter) call(ctx context.Context, conn domain.ManagementConnection, op func(token string, creds feishuCredentials) (status int, header http.Header, code int, msg string, err error)) error {
	creds, err := a.credentialsFor(ctx, conn)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		token, err := a.tenantToken(ctx, creds)
		if err != nil {
			return err
		}
		status, header, code, msg, callErr := op(token, creds)
		result := feishuResult{status: status, header: header, code: code, msg: msg, err: callErr}
		if result.success() {
			return nil
		}
		if result.tokenInvalid() {
			a.invalidateToken(creds)
			if attempt < a.maxRetries {
				continue
			}
		}
		if attempt < a.maxRetries && result.retryable() {
			if err := a.sleep(ctx, a.waitFor(result, attempt)); err != nil {
				return err
			}
			continue
		}
		return result.userError()
	}
}

func (a *FeishuAdapter) waitFor(result feishuResult, attempt int) time.Duration {
	if result.status == http.StatusTooManyRequests {
		if wait, ok := parseRateLimitReset(result.header.Get("x-ogw-ratelimit-reset"), a.now()); ok {
			return a.clampWait(wait)
		}
	}
	shift := attempt
	if shift > 8 {
		shift = 8
	}
	return a.clampWait(250 * time.Millisecond << uint(shift))
}

func (a *FeishuAdapter) clampWait(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	if d > a.maxRetryWait {
		return a.maxRetryWait
	}
	return d
}

// parseRateLimitReset accepts either an epoch timestamp or a delta in seconds.
func parseRateLimitReset(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	var wait time.Duration
	if seconds > 1e9 {
		wait = time.Unix(int64(seconds), 0).Sub(now)
	} else {
		wait = time.Duration(seconds * float64(time.Second))
	}
	if wait <= 0 {
		return 0, false
	}
	return wait, true
}

func (a *FeishuAdapter) tenantToken(ctx context.Context, creds feishuCredentials) (string, error) {
	key := creds.appID + "|" + secretDigest(creds.appSecret)
	now := a.now()
	a.tokenMu.Lock()
	if cached, ok := a.tokens[key]; ok && now.Before(cached.expires) {
		token := cached.value
		a.tokenMu.Unlock()
		return token, nil
	}
	a.tokenMu.Unlock()

	for attempt := 0; ; attempt++ {
		status, header, body, err := a.fetchToken(ctx, creds)
		result := feishuResult{status: status, header: header, err: err}
		if err == nil && status >= 200 && status < 300 {
			var payload struct {
				Code              int    `json:"code"`
				Msg               string `json:"msg"`
				TenantAccessToken string `json:"tenant_access_token"`
				Expire            int    `json:"expire"`
			}
			if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
				return "", adapterError(ErrFeishuUnavailable, "飞书租户令牌响应无法解析")
			}
			result.code, result.msg = payload.Code, payload.Msg
			if payload.Code == 0 && strings.TrimSpace(payload.TenantAccessToken) != "" {
				ttl := time.Duration(payload.Expire) * time.Second
				if ttl <= 0 {
					ttl = time.Hour
				}
				if ttl > time.Minute {
					ttl -= time.Minute
				}
				a.tokenMu.Lock()
				a.tokens[key] = cachedTenantToken{value: payload.TenantAccessToken, expires: now.Add(ttl)}
				a.tokenMu.Unlock()
				return payload.TenantAccessToken, nil
			}
		}
		if attempt < a.maxRetries && result.retryable() {
			if err := a.sleep(ctx, a.waitFor(result, attempt)); err != nil {
				return "", err
			}
			continue
		}
		return "", tokenError(result)
	}
}

func (a *FeishuAdapter) invalidateToken(creds feishuCredentials) {
	a.tokenMu.Lock()
	delete(a.tokens, creds.appID+"|"+secretDigest(creds.appSecret))
	a.tokenMu.Unlock()
}

func (a *FeishuAdapter) fetchToken(ctx context.Context, creds feishuCredentials) (int, http.Header, []byte, error) {
	payload, err := json.Marshal(map[string]string{"app_id": creds.appID, "app_secret": creds.appSecret})
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+feishuTokenPath, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, body, nil
}

func tokenError(result feishuResult) error {
	if result.err != nil {
		return adapterError(ErrFeishuUnavailable, "无法连接飞书开放平台：%v", result.err)
	}
	msg := strings.TrimSpace(result.msg)
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", result.status)
	}
	switch {
	case result.status == http.StatusTooManyRequests:
		return adapterError(ErrFeishuRateLimited, "飞书租户令牌获取触发限流，重试后仍失败：%s", msg)
	case result.status >= 500:
		return adapterError(ErrFeishuUnavailable, "飞书租户令牌获取失败（HTTP %d）：%s", result.status, msg)
	}
	return adapterError(ErrCredentialsUnavailable, "飞书拒绝了应用凭证（HTTP %d，code %d）：%s", result.status, result.code, msg)
}

// ---------------------------------------------------------------------------
// Field value helpers
// ---------------------------------------------------------------------------

func mappingFor(mappings []domain.ManagementFieldMapping, role domain.WorkItemField) (domain.ManagementFieldMapping, bool) {
	for _, mapping := range mappings {
		if mapping.Field == role {
			return mapping, true
		}
	}
	return domain.ManagementFieldMapping{}, false
}

func fieldName(mappings []domain.ManagementFieldMapping, role domain.WorkItemField) string {
	if mapping, ok := mappingFor(mappings, role); ok {
		return mapping.ExternalFieldName
	}
	return ""
}

func lookupRaw(fields map[string]interface{}, mapping domain.ManagementFieldMapping) (any, bool) {
	if fields == nil {
		return nil, false
	}
	if name := strings.TrimSpace(mapping.ExternalFieldName); name != "" {
		if value, ok := fields[name]; ok {
			return value, true
		}
	}
	if id := strings.TrimSpace(mapping.ExternalFieldID); id != "" {
		if value, ok := fields[id]; ok {
			return value, true
		}
	}
	return nil, false
}

func lookupField(fields map[string]interface{}, mapping domain.ManagementFieldMapping) (string, bool) {
	value, ok := lookupRaw(fields, mapping)
	if !ok {
		return "", false
	}
	return feishuFieldText(value), true
}

// feishuFieldText flattens the value shapes Bitable returns for text, select,
// person, number and link fields.
func feishuFieldText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := feishuFieldText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		for _, key := range []string{"text", "name", "en_name", "link", "value"} {
			if text, ok := v[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func feishuFieldURL(value any) string {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"link", "url", "text"} {
			if text, ok := v[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		return ""
	case []any:
		for _, item := range v {
			if url := feishuFieldURL(item); url != "" {
				return url
			}
		}
		return ""
	}
	return feishuFieldText(value)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}

func isReadOnlyTableError(code int, message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(message, "只读") || strings.Contains(message, "不支持写入") {
		return true
	}
	if strings.Contains(message, "同步") && (strings.Contains(message, "不支持") || strings.Contains(message, "只读")) {
		return true
	}
	for _, token := range []string{"read-only", "readonly", "synced table", "sync table", "not writable"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func isPermissionError(status, code int, message string) bool {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	switch code {
	case 1254301, 1254302, 91403, 99991679:
		return true
	}
	lower := strings.ToLower(message)
	return strings.Contains(lower, "permission") || strings.Contains(lower, "forbidden") ||
		strings.Contains(message, "无权") || strings.Contains(message, "可管理")
}
