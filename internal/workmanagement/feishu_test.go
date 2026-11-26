package workmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/domain"
)

// cannedResponse scripts one fake Feishu OpenAPI response.
type cannedResponse struct {
	Status int
	Header map[string]string
	Body   any
}

type recordedRequest struct {
	Key    string
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

// fakeFeishu is the single external seam: a scriptable in-memory OpenAPI.
type fakeFeishu struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	queue    map[string][]cannedResponse
	defaults map[string]any
	requests []recordedRequest
	calls    map[string]int
}

func newFakeFeishu(t *testing.T) *fakeFeishu {
	t.Helper()
	f := &fakeFeishu{
		t:        t,
		queue:    map[string][]cannedResponse{},
		defaults: map[string]any{},
		calls:    map[string]int{},
	}
	f.defaults["token"] = map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200}
	f.defaults["fields"] = map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": []any{}, "has_more": false}}
	f.defaults["search"] = map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": []any{}, "has_more": false}}
	f.defaults["batch_get"] = map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": []any{}}}
	f.defaults["batch_update"] = map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": []any{}}}
	f.defaults["subscribe"] = map[string]any{"code": 0, "msg": "ok", "data": map[string]any{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeFeishu) handle(w http.ResponseWriter, r *http.Request) {
	key := classifyPath(r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	record := recordedRequest{Key: key, Method: r.Method, Path: r.URL.Path, Query: r.URL.Query()}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &record.Body)
	}
	f.mu.Lock()
	f.calls[key]++
	f.requests = append(f.requests, record)
	var queued *cannedResponse
	if q := f.queue[key]; len(q) > 0 {
		next := q[0]
		f.queue[key] = q[1:]
		queued = &next
	}
	def := f.defaults[key]
	f.mu.Unlock()

	if key == "" {
		http.NotFound(w, r)
		return
	}
	resp := cannedResponse{Status: http.StatusOK, Body: def}
	if queued != nil {
		resp = *queued
		if resp.Body == nil {
			resp.Body = def
		}
	}
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	switch raw := resp.Body.(type) {
	case string:
		_, _ = w.Write([]byte(raw))
	default:
		_ = json.NewEncoder(w).Encode(raw)
	}
}

func classifyPath(path string) string {
	switch {
	case strings.Contains(path, "/auth/v3/tenant_access_token/internal"):
		return "token"
	case strings.Contains(path, "/records/search"):
		return "search"
	case strings.Contains(path, "/records/batch_get"):
		return "batch_get"
	case strings.Contains(path, "/records/batch_update"):
		return "batch_update"
	case strings.Contains(path, "/subscribe"):
		return "subscribe"
	case strings.HasSuffix(path, "/fields"):
		return "fields"
	}
	return ""
}

func (f *fakeFeishu) push(key string, responses ...cannedResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue[key] = append(f.queue[key], responses...)
}

func (f *fakeFeishu) setDefault(key string, body any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaults[key] = body
}

// setFieldType rewrites the numeric type of one field in the scripted field
// list, so a field_changed test can simulate a type change.
func (f *fakeFeishu) setFieldType(name string, fieldType int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := f.defaults["fields"].(map[string]any)
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		if item["field_name"] == name {
			item["type"] = fieldType
		}
	}
}

func (f *fakeFeishu) callCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeFeishu) lastRequest(key string) (recordedRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].Key == key {
			return f.requests[i], true
		}
	}
	return recordedRequest{}, false
}

type fakeMappingSource struct {
	fields   []domain.ManagementFieldMapping
	statuses []domain.ManagementStatusMapping
	err      error
}

func (f *fakeMappingSource) ManagementFieldMappings(connectionID, tableID string) ([]domain.ManagementFieldMapping, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.fields, nil
}

func (f *fakeMappingSource) ManagementStatusMappings(connectionID, tableID string) ([]domain.ManagementStatusMapping, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.statuses, nil
}

func testConnection() domain.ManagementConnection {
	return domain.ManagementConnection{
		ID: "conn-1", Name: "飞书需求表", Platform: domain.ManagementPlatformFeishuBitable,
		BotID: "bot-1", BaseAppToken: "bascn", TableID: "tbl-1", DefaultProjectID: "proj-1",
		AllowWriteback: true, Enabled: true,
	}
}

func standardMappings() []domain.ManagementFieldMapping {
	return []domain.ManagementFieldMapping{
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldBusinessKey, ExternalFieldName: "业务编号", ExternalFieldID: "fld-key"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldTitle, ExternalFieldName: "标题", ExternalFieldID: "fld-title"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldStatus, ExternalFieldName: "状态", ExternalFieldID: "fld-status"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldDescription, ExternalFieldName: "描述", ExternalFieldID: "fld-desc"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldPriority, ExternalFieldName: "优先级", ExternalFieldID: "fld-priority"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldAssignee, ExternalFieldName: "负责人", ExternalFieldID: "fld-assignee"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldAcceptanceCriteria, ExternalFieldName: "验收标准", ExternalFieldID: "fld-ac"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldRepository, ExternalFieldName: "仓库", ExternalFieldID: "fld-repo"},
		{ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldExternalURL, ExternalFieldName: "外链", ExternalFieldID: "fld-url"},
	}
}

func fakeFields(names ...string) map[string]any {
	items := make([]any, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]any{"field_name": name, "field_id": "fld-" + name, "type": 1, "ui_type": "Text"})
	}
	return map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": items, "has_more": false}}
}

type testSleep struct {
	durations []time.Duration
}

func (s *testSleep) sleep(_ context.Context, d time.Duration) error {
	s.durations = append(s.durations, d)
	return nil
}

func newTestAdapter(t *testing.T, fake *fakeFeishu, mappings *fakeMappingSource, mutate ...func(*AdapterOptions)) (*FeishuAdapter, *testSleep) {
	t.Helper()
	sleeper := &testSleep{}
	now := time.Unix(1_700_000_000, 0).UTC()
	opts := AdapterOptions{
		BaseURL:    fake.server.URL,
		Mappings:   mappings,
		MaxRetries: 3,
		Now:        func() time.Time { return now },
		Sleep:      sleeper.sleep,
		Credentials: func(context.Context, string) (string, string, error) {
			return "cli_test", "app-secret", nil
		},
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	return NewFeishuAdapter(opts), sleeper
}

func TestFeishuWritebackPausedAfterFieldTypeChange(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()}, func(o *AdapterOptions) {
		o.DeliveryFieldNames = map[string]string{"branch": "交付分支"}
	})
	conn := testConnection()
	conn.WritebackPaused = true
	conn.WritebackPausedReason = "状态（1 → 3）"

	if err := adapter.UpdateExternalStatus(context.Background(), conn, domain.ExternalRef{RecordID: "rec-1"}, "已完成"); !errors.Is(err, ErrWritebackPaused) {
		t.Fatalf("status writeback err = %v", err)
	}
	if err := adapter.UpdateDeliverySummary(context.Background(), conn, domain.ExternalRef{RecordID: "rec-1"}, domain.ExternalDeliverySummary{Branch: "x"}); !errors.Is(err, ErrWritebackPaused) {
		t.Fatalf("delivery writeback err = %v", err)
	}
	if fake.callCount("batch_update") != 0 {
		t.Fatalf("paused writeback still called Feishu: %+v", fake.calls)
	}
}

func TestFeishuValidateConnectionResolvesFieldTypes(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("fields", fakeFields("业务编号", "标题", "状态", "描述"))
	fake.setFieldType("状态", 3)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})

	_, resolved, err := adapter.ValidateAndResolve(context.Background(), testConnection())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, item := range resolved {
		if item.Field == domain.WorkItemFieldStatus && item.ExternalFieldType != "3" {
			t.Fatalf("field type not resolved: %+v", item)
		}
	}
}

func TestFeishuValidateConnectionResolvesFieldIDs(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("fields", fakeFields("业务编号", "标题", "状态", "描述"))
	mappings := &fakeMappingSource{fields: standardMappings()[:4]}
	adapter, _ := newTestAdapter(t, fake, mappings)

	readiness, resolved, err := adapter.ValidateAndResolve(context.Background(), testConnection())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if readiness.State != domain.ManagementReadinessReady {
		t.Fatalf("readiness = %+v", readiness)
	}
	byField := map[domain.WorkItemField]string{}
	for _, item := range resolved {
		byField[item.Field] = item.ExternalFieldID
	}
	if byField[domain.WorkItemFieldBusinessKey] != "fld-业务编号" || byField[domain.WorkItemFieldStatus] != "fld-状态" {
		t.Fatalf("resolved field ids = %+v", byField)
	}
	if fake.callCount("token") != 1 || fake.callCount("fields") != 1 {
		t.Fatalf("calls token=%d fields=%d", fake.callCount("token"), fake.callCount("fields"))
	}
}

func TestFeishuValidateConnectionReportsNotReady(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fields   []domain.ManagementFieldMapping
		table    map[string]any
		creds    func(context.Context, string) (string, string, error)
		wantIs   error
		wantText string
	}{
		{
			name:     "missing required mapping",
			fields:   standardMappings()[1:],
			table:    fakeFields("业务编号", "标题", "状态"),
			wantIs:   ErrFieldMapping,
			wantText: "缺少必填字段映射",
		},
		{
			name:     "mapped field absent from table",
			fields:   standardMappings()[:3],
			table:    fakeFields("业务编号", "标题"),
			wantIs:   ErrFieldMissing,
			wantText: "状态",
		},
		{
			name:   "credentials unavailable",
			fields: standardMappings()[:3],
			table:  fakeFields("业务编号", "标题", "状态"),
			creds: func(context.Context, string) (string, string, error) {
				return "", "", errors.New("通知机器人不存在")
			},
			wantIs:   ErrCredentialsUnavailable,
			wantText: "通知机器人不存在",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeFeishu(t)
			fake.setDefault("fields", tc.table)
			adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: tc.fields}, func(o *AdapterOptions) {
				if tc.creds != nil {
					o.Credentials = tc.creds
				}
			})
			readiness, _, err := adapter.ValidateAndResolve(context.Background(), testConnection())
			if err == nil || !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want %v", err, tc.wantIs)
			}
			if !strings.Contains(UserMessage(err), tc.wantText) {
				t.Fatalf("user message = %q, want contains %q", UserMessage(err), tc.wantText)
			}
			if readiness.State != domain.ManagementReadinessNotReady || strings.TrimSpace(readiness.Message) == "" {
				t.Fatalf("readiness = %+v", readiness)
			}
		})
	}
}

func TestFeishuGetWorkItemNormalizesMappedFields(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("batch_get", map[string]any{
		"code": 0, "msg": "ok",
		"data": map[string]any{"records": []any{map[string]any{
			"record_id":          "rec-1",
			"record_url":         "https://example.feishu.cn/base/rec-1",
			"last_modified_time": int64(1_700_000_123_000),
			"fields": map[string]any{
				"业务编号": "REQ-1",
				"标题":   []any{map[string]any{"type": "text", "text": "修复登录"}, map[string]any{"type": "text", "text": "缺陷"}},
				"状态":   "开发中",
				"描述":   "登录页报错",
				"优先级":  "P1",
				"负责人":  []any{map[string]any{"id": "ou_1", "name": "张三"}, map[string]any{"id": "ou_2", "name": "李四"}},
				"验收标准": "可以正常登录",
				"仓库":   "org/repo",
				"外链":   map[string]any{"link": "https://example.com/req/1", "text": "需求链接"},
			},
		}}},
	})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()})

	record, err := adapter.GetWorkItem(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if record.Title != "修复登录, 缺陷" || record.Description != "登录页报错" || record.Priority != "P1" {
		t.Fatalf("record text = %+v", record)
	}
	if record.Assignee != "张三, 李四" || record.AcceptanceCriteria != "可以正常登录" || record.Repository != "org/repo" {
		t.Fatalf("record detail = %+v", record)
	}
	if record.ExternalStatus != "开发中" || record.ExternalURL != "https://example.com/req/1" {
		t.Fatalf("record status/url = %+v", record)
	}
	if record.Ref.Source != domain.ManagementPlatformFeishuBitable || record.Ref.ConnectionID != "conn-1" ||
		record.Ref.TableID != "tbl-1" || record.Ref.BusinessKey != "REQ-1" || record.Ref.RecordID != "rec-1" {
		t.Fatalf("ref = %+v", record.Ref)
	}
	if !record.ModifiedAt.Equal(time.UnixMilli(1_700_000_123_000)) {
		t.Fatalf("modified at = %v", record.ModifiedAt)
	}
	req, _ := fake.lastRequest("batch_get")
	ids, _ := req.Body["record_ids"].([]any)
	if len(ids) != 1 || ids[0] != "rec-1" {
		t.Fatalf("batch_get body = %+v", req.Body)
	}
}

func TestFeishuGetWorkItemSearchesByBusinessKey(t *testing.T) {
	record := map[string]any{
		"record_id": "rec-9",
		"fields":    map[string]any{"业务编号": "REQ-9", "标题": "新需求", "状态": "草稿"},
	}
	fake := newFakeFeishu(t)
	fake.setDefault("search", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{
		"items": []any{record}, "has_more": false,
	}})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})

	got, err := adapter.GetWorkItem(context.Background(), testConnection(), domain.ExternalRef{BusinessKey: "REQ-9"})
	if err != nil || got.Ref.RecordID != "rec-9" || got.Title != "新需求" {
		t.Fatalf("search by business key = %+v, %v", got, err)
	}
	req, ok := fake.lastRequest("search")
	if !ok {
		t.Fatal("search was not called")
	}
	filter, _ := req.Body["filter"].(map[string]any)
	conditions, _ := filter["conditions"].([]any)
	if len(conditions) != 1 {
		t.Fatalf("filter = %+v", filter)
	}
	condition, _ := conditions[0].(map[string]any)
	if condition["field_name"] != "业务编号" || condition["operator"] != "is" {
		t.Fatalf("condition = %+v", condition)
	}
}

func TestFeishuGetWorkItemRejectsRecordWithoutBusinessKey(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("batch_get", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": []any{map[string]any{
		"record_id": "rec-1", "fields": map[string]any{"标题": "没有编号", "状态": "草稿"},
	}}}})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})

	_, err := adapter.GetWorkItem(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"})
	if !errors.Is(err, ErrBusinessKeyMissing) || !strings.Contains(UserMessage(err), "业务编号") {
		t.Fatalf("err = %v", err)
	}
}

func TestFeishuListWorkItemsPagesAndResolvesStatus(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("search", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{
		"items": []any{
			map[string]any{"record_id": "rec-1", "fields": map[string]any{"业务编号": "REQ-1", "标题": "一", "状态": "开发中"}},
			map[string]any{"record_id": "rec-2", "fields": map[string]any{"业务编号": "REQ-2", "标题": "二", "状态": "草稿"}},
		},
		"has_more": true, "page_token": "page-2",
	}})
	statuses := []domain.ManagementStatusMapping{
		{ConnectionID: "conn-1", TableID: "tbl-1", ExternalStatus: "开发中", Action: domain.ManagementActionStartSOP, WorkflowID: "sop-1"},
	}
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3], statuses: statuses})

	page, err := adapter.ListWorkItems(context.Background(), testConnection(), "", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Records) != 2 || !page.HasMore || page.Cursor != "page-2" {
		t.Fatalf("page = %+v", page)
	}
	req, _ := fake.lastRequest("search")
	if req.Body["automatic_fields"] != true {
		t.Fatalf("automatic_fields missing: %+v", req.Body)
	}

	mapping, known, err := adapter.ResolveStatusMapping(context.Background(), testConnection(), "开发中")
	if err != nil || !known || mapping.Action != domain.ManagementActionStartSOP || mapping.WorkflowID != "sop-1" {
		t.Fatalf("known status = %+v, %v, %v", mapping, known, err)
	}
	if _, known, err := adapter.ResolveStatusMapping(context.Background(), testConnection(), "未知状态"); err != nil || known {
		t.Fatalf("unknown status must not trigger: known=%v err=%v", known, err)
	}
}

func TestFeishuStatusActionForUnknownStatusIsEmpty(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3], statuses: []domain.ManagementStatusMapping{
		{ConnectionID: "conn-1", TableID: "tbl-1", ExternalStatus: "开发中", Action: domain.ManagementActionStartSOP, WorkflowID: "sop-1"},
	}})
	if _, known, err := adapter.ResolveStatusMapping(context.Background(), testConnection(), ""); err != nil || known {
		t.Fatalf("empty status: known=%v err=%v", known, err)
	}
}

func TestFeishuWritebackDisabledWritesNothing(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()})
	conn := testConnection()
	conn.AllowWriteback = false

	err := adapter.UpdateExternalStatus(context.Background(), conn, domain.ExternalRef{RecordID: "rec-1"}, "已完成")
	if !errors.Is(err, ErrWritebackDisabled) {
		t.Fatalf("err = %v", err)
	}
	if fake.callCount("batch_update") != 0 || fake.callCount("token") != 0 {
		t.Fatalf("writeback disabled still called Feishu: %+v", fake.calls)
	}
}

func TestFeishuUpdateExternalStatusWritesOnlyMappedStatus(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()})

	if err := adapter.UpdateExternalStatus(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"}, "已完成"); err != nil {
		t.Fatalf("update: %v", err)
	}
	req, ok := fake.lastRequest("batch_update")
	if !ok {
		t.Fatal("batch_update was not called")
	}
	records, _ := req.Body["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("records = %+v", req.Body)
	}
	record, _ := records[0].(map[string]any)
	if record["record_id"] != "rec-1" {
		t.Fatalf("record id = %+v", record)
	}
	fields, _ := record["fields"].(map[string]any)
	if len(fields) != 1 || fields["状态"] != "已完成" {
		t.Fatalf("writeback fields = %+v", fields)
	}
}

func TestFeishuUpdateDeliverySummaryWritesOnlyConfiguredFields(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()}, func(o *AdapterOptions) {
		o.DeliveryFieldNames = map[string]string{"branch": "交付分支", "commit": "Commit SHA", "url": "交付链接"}
	})
	summary := domain.ExternalDeliverySummary{Branch: "feat/x", CommitSHA: "abc123", URL: "https://example.com/mr/1", Tests: "ignored"}
	if err := adapter.UpdateDeliverySummary(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"}, summary); err != nil {
		t.Fatalf("update delivery: %v", err)
	}
	req, _ := fake.lastRequest("batch_update")
	records, _ := req.Body["records"].([]any)
	record, _ := records[0].(map[string]any)
	fields, _ := record["fields"].(map[string]any)
	if len(fields) != 3 || fields["交付分支"] != "feat/x" || fields["Commit SHA"] != "abc123" || fields["交付链接"] != "https://example.com/mr/1" {
		t.Fatalf("delivery fields = %+v", fields)
	}
	if _, ok := fields["测试"]; ok {
		t.Fatalf("unmapped summary was written: %+v", fields)
	}
}

func TestFeishuUpdateDeliverySummaryWithoutMappingIsReadableError(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()})
	err := adapter.UpdateDeliverySummary(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"}, domain.ExternalDeliverySummary{Branch: "x"})
	if !errors.Is(err, ErrDeliveryWritebackNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if fake.callCount("batch_update") != 0 {
		t.Fatal("unexpected write")
	}
}

func TestFeishuRetriesTransientFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, tc := range []struct {
		name       string
		failures   []cannedResponse
		wantErr    bool
		wantSleeps int
		wantWait   time.Duration
	}{
		{
			name:       "500 then success",
			failures:   []cannedResponse{{Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}}},
			wantSleeps: 1,
			wantWait:   250 * time.Millisecond,
		},
		{
			name: "429 honors reset header then success",
			failures: []cannedResponse{{
				Status: http.StatusTooManyRequests,
				Header: map[string]string{"x-ogw-ratelimit-reset": "1700000005"},
				Body:   map[string]any{"code": 99991400, "msg": "rate limited"},
			}},
			wantSleeps: 1,
			wantWait:   5 * time.Second,
		},
		{
			name:       "500 exhausted",
			failures:   []cannedResponse{{Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}}, {Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}}, {Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}}, {Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}}},
			wantErr:    true,
			wantSleeps: 3,
			wantWait:   250 * time.Millisecond,
		},
		{
			name:       "400 is not retried",
			failures:   []cannedResponse{{Status: http.StatusBadRequest, Body: map[string]any{"code": 1254000, "msg": "bad request"}}},
			wantErr:    true,
			wantSleeps: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeFeishu(t)
			fake.push("search", tc.failures...)
			adapter, sleeper := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]}, func(o *AdapterOptions) {
				o.Now = func() time.Time { return now }
			})
			_, err := adapter.ListWorkItems(context.Background(), testConnection(), "", 10)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(sleeper.durations) != tc.wantSleeps {
				t.Fatalf("sleeps = %v, want %d", sleeper.durations, tc.wantSleeps)
			}
			if tc.wantWait > 0 && sleeper.durations[0] != tc.wantWait {
				t.Fatalf("first wait = %v, want %v", sleeper.durations[0], tc.wantWait)
			}
		})
	}
}

func TestFeishuTokenFetchRetriesAndCaches(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("search", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": []any{}, "has_more": false}})
	fake.push("token", cannedResponse{Status: http.StatusInternalServerError, Body: map[string]any{"code": 99991400, "msg": "internal error"}})
	adapter, sleeper := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})

	for i := 0; i < 2; i++ {
		if _, err := adapter.ListWorkItems(context.Background(), testConnection(), "", 10); err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
	}
	if fake.callCount("token") != 2 {
		t.Fatalf("token calls = %d, want 2 (one failure + one success, then cached)", fake.callCount("token"))
	}
	if len(sleeper.durations) != 1 {
		t.Fatalf("token retry sleeps = %v", sleeper.durations)
	}
}

func TestFeishuReadOnlyTableError(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("batch_update", map[string]any{"code": 1254003, "msg": "数据表来自其他数据源同步，不支持写入"})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()})

	err := adapter.UpdateExternalStatus(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-1"}, "已完成")
	if !errors.Is(err, ErrReadOnlyTable) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(UserMessage(err), "只读") && !strings.Contains(UserMessage(err), "同步") {
		t.Fatalf("user message = %q", UserMessage(err))
	}
}

func TestFeishuPermissionErrorIsReadable(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("search", map[string]any{"code": 1254302, "msg": "permission denied"})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})
	_, err := adapter.ListWorkItems(context.Background(), testConnection(), "", 10)
	if err == nil || !strings.Contains(UserMessage(err), "可管理者") {
		t.Fatalf("err = %v, message = %q", err, UserMessage(err))
	}
}

func TestFeishuSubscribeEventsUsesFileTokenAndBitableType(t *testing.T) {
	fake := newFakeFeishu(t)
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})

	if err := adapter.SubscribeEvents(context.Background(), testConnection()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	req, ok := fake.lastRequest("subscribe")
	if !ok {
		t.Fatal("subscribe endpoint was not called")
	}
	if req.Method != http.MethodPost || !strings.HasSuffix(req.Path, "/files/bascn/subscribe") {
		t.Fatalf("subscribe request = %+v", req)
	}
	if got := req.Query.Get("file_type"); got != "bitable" {
		t.Fatalf("file_type = %q", got)
	}
}

func TestFeishuSubscribeEventsReportsPermission(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("subscribe", map[string]any{"code": 1061045, "msg": "no permission"})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:3]})
	err := adapter.SubscribeEvents(context.Background(), testConnection())
	if err == nil || !errors.Is(err, ErrFeishuPermission) {
		t.Fatalf("err = %v", err)
	}
}

// The projection read is deliberately lenient: a record without a business
// key or title still reaches the Work Item projection, which raises an alert
// and degrades to the record id instead of rejecting the record.
func TestFeishuReadRecordForProjectionDegradesMissingIdentity(t *testing.T) {
	fake := newFakeFeishu(t)
	fake.setDefault("batch_get", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": []any{map[string]any{
		"record_id": "rec-7", "fields": map[string]any{"状态": "草稿", "描述": "公式结果"},
	}}}})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: standardMappings()[:4]})

	record, err := adapter.ReadRecordForProjection(context.Background(), testConnection(), "rec-7")
	if err != nil {
		t.Fatalf("read for projection: %v", err)
	}
	if record.Ref.BusinessKey != "" || record.Ref.RecordID != "rec-7" || record.Title != "rec-7" {
		t.Fatalf("degraded record = %+v", record)
	}
	if record.Description != "公式结果" || record.ExternalStatus != "草稿" {
		t.Fatalf("record fields = %+v", record)
	}
	if _, err := adapter.GetWorkItem(context.Background(), testConnection(), domain.ExternalRef{RecordID: "rec-7"}); !errors.Is(err, ErrBusinessKeyMissing) {
		t.Fatalf("strict read must keep rejecting: %v", err)
	}
}

func TestFeishuReadRecordForProjectionResolvesProjectOverride(t *testing.T) {
	mappings := append(standardMappings()[:4], domain.ManagementFieldMapping{
		ConnectionID: "conn-1", TableID: "tbl-1", Field: domain.WorkItemFieldProject,
		ExternalFieldName: "项目", ExternalFieldID: "fld-project",
	})
	fake := newFakeFeishu(t)
	fake.setDefault("batch_get", map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": []any{map[string]any{
		"record_id": "rec-1", "fields": map[string]any{"业务编号": "REQ-1", "标题": "需求", "项目": "proj-other"},
	}}}})
	adapter, _ := newTestAdapter(t, fake, &fakeMappingSource{fields: mappings})
	record, err := adapter.ReadRecordForProjection(context.Background(), testConnection(), "rec-1")
	if err != nil || record.ProjectID != "proj-other" {
		t.Fatalf("project override = %+v %v", record, err)
	}
}
