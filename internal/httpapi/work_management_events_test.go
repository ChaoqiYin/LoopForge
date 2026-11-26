package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/auth"
	"agentflow/internal/domain"
	"agentflow/internal/engine"
	"agentflow/internal/notification"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"

	"github.com/gorilla/websocket"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// fakeBitableFeishu is the single external seam for event tests: one local
// origin serves the Bitable OpenAPI and the Feishu long-connection protocol.
type fakeBitableFeishu struct {
	t   *testing.T
	srv *httptest.Server

	mu             sync.Mutex
	records        map[string]map[string]any
	searchItems    []map[string]any
	subscribeCalls int
	batchGetCalls  int
	searchCalls    int
	// batchGetGate, when non-nil, blocks record reads until it is closed.
	batchGetGate chan struct{}
	requests     []fakeBitableRequest

	conns  chan *websocket.Conn
	acks   chan int
	closed chan struct{}
}

// fakeBitableRequest is one recorded OpenAPI call; the writeback test asserts
// the outgoing fields without reaching into the adapter.
type fakeBitableRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

func newFakeBitableFeishu(t *testing.T) *fakeBitableFeishu {
	t.Helper()
	f := &fakeBitableFeishu{
		t: t, records: map[string]map[string]any{},
		conns: make(chan *websocket.Conn, 4), acks: make(chan int, 32), closed: make(chan struct{}, 4),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/", f.openAPI)
	mux.HandleFunc("/callback/ws/endpoint", func(w http.ResponseWriter, r *http.Request) {
		url := "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws?device_id=d1&service_id=1"
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"URL": url, "ClientConfig": map[string]any{"PingInterval": 120, "ReconnectCount": -1, "ReconnectInterval": 120, "ReconnectNonce": 1}}})
	})
	var upgrader websocket.Upgrader
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.conns <- c
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				f.closed <- struct{}{}
				return
			}
			var frame larkws.Frame
			if frame.Unmarshal(raw) != nil || frame.Method != int32(larkws.FrameTypeData) {
				continue
			}
			var resp struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(frame.Payload, &resp)
			f.acks <- resp.Code
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBitableFeishu) setRecord(recordID string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[recordID] = fields
}

func (f *fakeBitableFeishu) setSearch(items []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchItems = append([]map[string]any(nil), items...)
}

func (f *fakeBitableFeishu) openAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		f.mu.Lock()
		f.requests = append(f.requests, fakeBitableRequest{Method: r.Method, Path: r.URL.Path, Body: parsed})
		f.mu.Unlock()
	}
	switch {
	case strings.Contains(r.URL.Path, "/auth/v3/tenant_access_token/internal"):
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":7200}`))
	case strings.HasSuffix(r.URL.Path, "/fields"):
		items := []any{}
		for _, name := range []string{"业务编号", "标题", "状态", "描述", "验收标准", "负责人", "项目", "仓库", "外链"} {
			items = append(items, map[string]any{"field_name": name, "field_id": "fld-" + name, "type": 1})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": items, "has_more": false}})
	case strings.Contains(r.URL.Path, "/records/batch_get"):
		f.mu.Lock()
		f.batchGetCalls++
		gate := f.batchGetGate
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		var body struct {
			RecordIDs []string `json:"record_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		records := make([]any, 0, len(body.RecordIDs))
		for _, id := range body.RecordIDs {
			if fields, ok := f.records[id]; ok {
				records = append(records, map[string]any{"record_id": id, "record_url": "https://example.feishu.cn/base/" + id, "fields": fields})
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"records": records}})
	case strings.Contains(r.URL.Path, "/records/search"):
		f.mu.Lock()
		f.searchCalls++
		items := append([]map[string]any(nil), f.searchItems...)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": items, "has_more": false}})
	case strings.Contains(r.URL.Path, "/records/batch_update"):
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"records":[]}}`))
	case strings.Contains(r.URL.Path, "/subscribe"):
		f.mu.Lock()
		f.subscribeCalls++
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
	default:
		http.NotFound(w, r)
	}
}

// lastRequest returns the most recent recorded call whose path contains key.
func (f *fakeBitableFeishu) lastRequest(key string) (fakeBitableRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if strings.Contains(f.requests[i].Path, key) {
			return f.requests[i], true
		}
	}
	return fakeBitableRequest{}, false
}

func (f *fakeBitableFeishu) waitConn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-f.conns:
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("feishu long connection was not established")
		return nil
	}
}

func (f *fakeBitableFeishu) push(t *testing.T, c *websocket.Conn, payload string) {
	t.Helper()
	frame := larkws.Frame{
		SeqID: 1, LogID: 1, Service: 1, Method: int32(larkws.FrameTypeData),
		Headers: []larkws.Header{
			{Key: "type", Value: "event"}, {Key: "message_id", Value: "frame-" + payload},
			{Key: "sum", Value: "1"}, {Key: "seq", Value: "0"}, {Key: "trace_id", Value: "trace"},
		},
		Payload: []byte(payload),
	}
	raw, err := frame.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeBitableFeishu) waitAck(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-f.acks:
		return code
	case <-time.After(timeout):
		t.Fatal("no acknowledgement from the feishu long connection")
		return 0
	}
}

func recordChangedJSON(eventID, recordID, action string, afterFields ...map[string]any) string {
	after := make([]any, 0, len(afterFields))
	for _, field := range afterFields {
		after = append(after, field)
	}
	payload, _ := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id": eventID, "event_type": "drive.file.bitable_record_changed_v1",
			"app_id": "cli_test", "tenant_key": "tenant-1", "create_time": "1794970000000",
		},
		"event": map[string]any{
			"file_type": "bitable", "file_token": "bascn", "table_id": "tbl-1",
			"action_list": []any{map[string]any{
				"record_id": recordID, "action": action, "after_value": after,
			}},
		},
	})
	return string(payload)
}

func fieldChangedJSON(eventID string) string {
	payload, _ := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id": eventID, "event_type": "drive.file.bitable_field_changed_v1",
			"app_id": "cli_test", "tenant_key": "tenant-1", "create_time": "1794970000000",
		},
		"event": map[string]any{"file_type": "bitable", "file_token": "bascn", "table_id": "tbl-1", "action_list": []any{}},
	})
	return string(payload)
}

type workManagementEventTestServer struct {
	store *repo.Store
	gin   http.Handler
	auth  *auth.Client
	token string
	fake  *fakeBitableFeishu
	ws    *websocket.Conn
	ctx   context.Context
}

func newWorkManagementEventTestServer(t *testing.T) *workManagementEventTestServer {
	t.Helper()
	t.Setenv("AGENTFLOW_PLATFORM_ADMINS", "demo-owner")
	t.Setenv("AGENTFLOW_SESSION_SECRET", "work-management-events-secret")
	fake := newFakeBitableFeishu(t)
	t.Setenv("AGENTFLOW_FEISHU_BASE_URL", fake.srv.URL)

	store, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	bot := domain.NotificationBot{
		ID: "bot-feishu", ProjectID: "platform", OwnerID: 1, Channel: domain.BotChannelFeishu,
		BotID: "cli_test", Name: "飞书应用", Enabled: true, InteractionEnabled: true,
	}
	if err := store.SaveNotificationBot(bot, "app-secret"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng := engine.New(store, runtime.NewManager(filepath.Join(t.TempDir(), "workspaces")))
	notifications := notification.New(store)
	router, _ := NewWithNotificationsAndBackground(ctx, store, eng, tracker.NewService(), "", "", notifications, BackgroundOptions{})
	notifications.Start(ctx)

	authClient := auth.NewClientFromEnv()
	user, ok := store.UserByLoginName("demo-owner")
	if !ok {
		t.Fatal("missing demo-owner")
	}
	token, err := authClient.SignUser(&auth.User{ID: user.ID, LoginName: user.LoginName, AuthTime: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return &workManagementEventTestServer{
		store: store, gin: router, auth: authClient, token: token, fake: fake,
		ws: fake.waitConn(t), ctx: ctx,
	}
}

func (s *workManagementEventTestServer) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: s.token})
	rec := httptest.NewRecorder()
	s.gin.ServeHTTP(rec, req)
	return rec
}

func (s *workManagementEventTestServer) configure(t *testing.T) string {
	t.Helper()
	rec := s.request(t, http.MethodPut, "/api/work-management/connections", validWorkManagementConnection)
	if rec.Code != http.StatusOK {
		t.Fatalf("create connection = %d %s", rec.Code, rec.Body.String())
	}
	connectionID := decodeWorkManagementResponse(t, rec)["connection"].(map[string]any)["id"].(string)
	mappings := `{"fieldMappings":[
	 {"field":"business_key","externalFieldName":"业务编号"},
	 {"field":"title","externalFieldName":"标题"},
	 {"field":"status","externalFieldName":"状态"},
	 {"field":"description","externalFieldName":"描述"},
	 {"field":"assignee","externalFieldName":"负责人"},
	 {"field":"project","externalFieldName":"项目"},
	 {"field":"repository","externalFieldName":"仓库"},
	 {"field":"external_url","externalFieldName":"外链"}
	]}`
	if rec := s.request(t, http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", mappings); rec.Code != http.StatusOK {
		t.Fatalf("field mappings = %d %s", rec.Code, rec.Body.String())
	}
	return connectionID
}

// issuesFromAPI reads the Work Item projection through the public HTTP API,
// not through the store: the projection contract is what a client sees.
func (s *workManagementEventTestServer) issuesFromAPI(t *testing.T) []domain.Issue {
	t.Helper()
	rec := s.request(t, http.MethodGet, "/api/issues", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("issues API = %d %s", rec.Code, rec.Body.String())
	}
	var issues []domain.Issue
	if err := json.Unmarshal(rec.Body.Bytes(), &issues); err != nil {
		t.Fatalf("decode issues: %v (%s)", err, rec.Body.String())
	}
	return issues
}

func (s *workManagementEventTestServer) issueByBusinessKey(t *testing.T, key string) (domain.Issue, bool) {
	t.Helper()
	for _, issue := range s.issuesFromAPI(t) {
		if issue.ExternalRef != nil && issue.ExternalRef.BusinessKey == key {
			return issue, true
		}
	}
	return domain.Issue{}, false
}

func (s *workManagementEventTestServer) workItemCount(t *testing.T) int {
	t.Helper()
	count := 0
	for _, issue := range s.issuesFromAPI(t) {
		if issue.ExternalRef != nil && issue.ExternalRef.Source == domain.ManagementPlatformFeishuBitable {
			count++
		}
	}
	return count
}

func (s *workManagementEventTestServer) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestWorkManagementRecordEventProjectsWorkItem(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	s.fake.setRecord("rec-1", map[string]any{
		"业务编号": "REQ-1", "标题": "修复登录", "状态": "开发中",
		"描述": "登录页报错", "负责人": "张三", "仓库": "org/repo", "外链": "https://example.com/req/1",
	})

	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("ack = %d", code)
	}
	var projected domain.Issue
	s.waitFor(t, "projected work item", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		if !ok {
			return false
		}
		projected = issue
		return true
	})
	if projected.Title != "修复登录" || projected.Description != "登录页报错" ||
		projected.ProjectID != "platform" || projected.Kind != "requirement" ||
		projected.ExternalAssignee != "张三" || projected.AcceptanceCriteria != "" {
		t.Fatalf("projection = %+v", projected)
	}
	if projected.ExternalRef == nil || projected.ExternalRef.ExternalState != "开发中" ||
		projected.ExternalRef.ExternalURL != "https://example.com/req/1" ||
		projected.ExternalRef.RecordID != "rec-1" || projected.ExternalRef.Orphaned {
		t.Fatalf("external ref = %+v", projected.ExternalRef)
	}
	if projected.Status != "backlog" {
		t.Fatalf("local lifecycle status = %q", projected.Status)
	}

	// The Work Item is visible through the public HTTP API.
	rec := s.request(t, http.MethodGet, "/api/issues?projectId=platform", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "REQ-1") {
		t.Fatalf("issues API = %d %s", rec.Code, rec.Body.String())
	}

	// Edited event updates the projection without creating a second Work Item.
	s.fake.setRecord("rec-1", map[string]any{
		"业务编号": "REQ-1", "标题": "修复登录（更新）", "状态": "已完成", "描述": "登录页报错",
	})
	s.fake.push(t, s.ws, recordChangedJSON("evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-标题", "field_value": `"修复登录（更新）"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("edit ack = %d", code)
	}
	s.waitFor(t, "edited title", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.Title == "修复登录（更新）" && issue.ExternalRef.ExternalState == "已完成"
	})
	if count := s.workItemCount(t); count != 1 {
		t.Fatalf("work items = %d, want 1", count)
	}

	// At-least-once delivery: the same event id is acknowledged but not
	// projected twice.
	s.fake.push(t, s.ws, recordChangedJSON("evt-2", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-标题", "field_value": `"修复登录（更新）"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("duplicate ack = %d", code)
	}
	time.Sleep(300 * time.Millisecond)
	if count := s.workItemCount(t); count != 1 {
		t.Fatalf("duplicate event created %d work items", count)
	}

	// Out-of-order delivery: an older event read after the newer state still
	// converges because projection always reads the authoritative record.
	s.fake.push(t, s.ws, recordChangedJSON("evt-0", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-标题", "field_value": `"旧标题"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("out-of-order ack = %d", code)
	}
	s.waitFor(t, "out-of-order convergence", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.Title == "修复登录（更新）"
	})
}

func TestWorkManagementDeleteEventOrphansWorkItemAndKeepsEvidence(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "待删除", "状态": "开发中"})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("added ack = %d", code)
	}
	s.waitFor(t, "projected work item", func() bool {
		_, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok
	})

	s.fake.push(t, s.ws, recordChangedJSON("evt-2", "rec-1", "record_deleted"))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("deleted ack = %d", code)
	}
	s.waitFor(t, "orphaned work item", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.ExternalRef != nil && issue.ExternalRef.Orphaned
	})
	issue, _ := s.issueByBusinessKey(t, "REQ-1")
	if issue.Status != "backlog" {
		t.Fatalf("orphaning must not change the local lifecycle: %+v", issue)
	}
}

func TestWorkManagementAckDoesNotWaitForProjection(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "慢投影", "状态": "草稿"})
	gate := make(chan struct{})
	s.fake.mu.Lock()
	s.fake.batchGetGate = gate
	s.fake.mu.Unlock()

	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 1500*time.Millisecond); code != http.StatusOK {
		t.Fatalf("ack waited for projection: %d", code)
	}
	if _, ok := s.issueByBusinessKey(t, "REQ-1"); ok {
		t.Fatal("work item projected before the external read was released")
	}
	close(gate)
	s.waitFor(t, "projection after release", func() bool {
		_, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok
	})
}

func TestWorkManagementBusinessKeyConflictRebindsRecord(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	// Two rows carry the same business key. The business key is the identity:
	// both project to one Work Item and the record pointer follows the newest
	// authoritative row.
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "第一行", "状态": "草稿"})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("first ack = %d", code)
	}
	s.waitFor(t, "first projection", func() bool {
		_, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok
	})

	s.fake.setRecord("rec-2", map[string]any{"业务编号": "REQ-1", "标题": "第二行", "状态": "草稿"})
	s.fake.push(t, s.ws, recordChangedJSON("evt-2", "rec-2", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("second ack = %d", code)
	}
	s.waitFor(t, "rebound record", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.ExternalRef.RecordID == "rec-2" && issue.Title == "第二行"
	})
	if count := s.workItemCount(t); count != 1 {
		t.Fatalf("business key conflict created %d work items", count)
	}
}

func TestWorkManagementFormulaFieldIsReadBack(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	// The event body omits the formula-backed description, as Feishu does.
	s.fake.setRecord("rec-1", map[string]any{
		"业务编号": "REQ-1", "标题": "新标题", "状态": "草稿", "描述": "公式结果",
	})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_edited",
		map[string]any{"field_id": "fld-标题", "field_value": `"新标题"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("ack = %d", code)
	}
	s.waitFor(t, "formula description", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.Description == "公式结果"
	})
	s.fake.mu.Lock()
	calls := s.fake.batchGetCalls
	s.fake.mu.Unlock()
	if calls == 0 {
		t.Fatal("record was never read back for the formula field")
	}
}

func TestWorkManagementManualSyncOrphansRecordsMissingRemotely(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configure(t)
	s.fake.setRecord("rec-1", map[string]any{"业务编号": "REQ-1", "标题": "本地有", "状态": "草稿"})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("ack = %d", code)
	}
	s.waitFor(t, "projected work item", func() bool {
		_, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok
	})

	// The remote listing no longer contains the record and no delete event
	// was delivered.
	s.fake.setSearch(nil)
	rec := s.request(t, http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeWorkManagementResponse(t, rec)
	syncBody, _ := body["sync"].(map[string]any)
	if orphaned, _ := syncBody["orphaned"].(float64); orphaned < 1 {
		t.Fatalf("sync summary = %+v", body)
	}
	s.waitFor(t, "orphaned by reconciliation", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.ExternalRef != nil && issue.ExternalRef.Orphaned
	})
}

func TestWorkManagementMissingBusinessKeyDegradesWithAlert(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	// No 业务编号 field: the adapter read is lenient for projection and the
	// record id becomes the stable identity.
	s.fake.setRecord("rec-9", map[string]any{"标题": "没有编号", "状态": "草稿"})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-9", "record_added",
		map[string]any{"field_id": "fld-标题", "field_value": `"没有编号"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("ack = %d", code)
	}
	s.waitFor(t, "degraded work item", func() bool {
		issue, ok := s.issueByBusinessKey(t, "rec-9")
		return ok && issue.ExternalRef != nil && issue.ExternalRef.RecordID == "rec-9"
	})
}

func TestWorkManagementConnectionSubscribesFile(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configure(t)
	// Saving an enabled connection subscribes its file; verification repeats
	// the self-check and subscription so an administrator can repair it.
	rec := s.request(t, http.MethodPost, "/api/work-management/connections/"+connectionID+"/verify", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	s.fake.mu.Lock()
	calls := s.fake.subscribeCalls
	s.fake.mu.Unlock()
	if calls == 0 {
		t.Fatal("bitable file was not subscribed")
	}
}

func TestWorkManagementProjectFieldOverridesConnectionDefault(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	s.configure(t)
	now := time.Now().UTC()
	if err := s.store.PutProject(domain.Project{
		ID: "proj-other", Key: "OTHER", Name: "其他项目", OwnerID: 1,
		Members: []domain.ProjectMember{{UserID: 1, Role: "maintainer"}}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	s.fake.setRecord("rec-1", map[string]any{
		"业务编号": "REQ-1", "标题": "跨项目需求", "状态": "草稿", "项目": "OTHER",
	})
	s.fake.push(t, s.ws, recordChangedJSON("evt-1", "rec-1", "record_added",
		map[string]any{"field_id": "fld-业务编号", "field_value": `"REQ-1"`}))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("ack = %d", code)
	}
	s.waitFor(t, "project override", func() bool {
		issue, ok := s.issueByBusinessKey(t, "REQ-1")
		return ok && issue.ProjectID == "proj-other"
	})
}

func TestWorkManagementFieldChangedEventRefreshesFieldIDs(t *testing.T) {
	s := newWorkManagementEventTestServer(t)
	connectionID := s.configure(t)
	// Stale cache: clear the resolved field ids directly so the event has
	// something to refresh.
	conn, _ := s.store.ManagementConnection(connectionID)
	stored, err := s.store.ManagementFieldMappings(conn.ID, conn.TableID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range stored {
		stored[i].ExternalFieldID = ""
	}
	if err := s.store.PutManagementFieldMappings(stored); err != nil {
		t.Fatal(err)
	}
	s.fake.mu.Lock()
	before := s.fake.searchCalls
	s.fake.mu.Unlock()
	s.fake.push(t, s.ws, fieldChangedJSON("evt-field-1"))
	if code := s.fake.waitAck(t, 3*time.Second); code != http.StatusOK {
		t.Fatalf("field changed ack = %d", code)
	}
	// The mapping refresh resolves external field ids against the table; the
	// client sees the refreshed cache through the configuration API.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := s.request(t, http.MethodGet, "/api/work-management/connections/"+connectionID+"/field-mappings", "")
		if rec.Code == http.StatusOK {
			body := decodeWorkManagementResponse(t, rec)
			mappings, _ := body["fieldMappings"].([]any)
			for _, raw := range mappings {
				item, _ := raw.(map[string]any)
				if id, _ := item["externalFieldId"].(string); id != "" {
					goto refreshed
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("field id cache was not refreshed")
		}
		time.Sleep(25 * time.Millisecond)
	}
refreshed:
	s.fake.mu.Lock()
	after := s.fake.searchCalls
	s.fake.mu.Unlock()
	if after != before {
		t.Fatalf("field change must not run reconciliation search: before=%d after=%d", before, after)
	}
}
