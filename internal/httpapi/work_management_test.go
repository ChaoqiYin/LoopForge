package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/auth"
	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"agentflow/internal/workmanagement"

	"github.com/gin-gonic/gin"
)

// fakeWorkManagementFeishu scripts just the two OpenAPI endpoints this
// ticket's HTTP surface touches: the tenant token and the field list.
type fakeWorkManagementFeishu struct {
	mu         sync.Mutex
	fields     []string
	tokenCalls int
}

func (f *fakeWorkManagementFeishu) setFields(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fields = names
}

func (f *fakeWorkManagementFeishu) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(r.URL.Path, "/auth/v3/tenant_access_token/internal"):
		f.mu.Lock()
		f.tokenCalls++
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":7200}`))
	case strings.HasSuffix(r.URL.Path, "/fields"):
		f.mu.Lock()
		names := append([]string(nil), f.fields...)
		f.mu.Unlock()
		items := make([]map[string]any, 0, len(names))
		for _, name := range names {
			items = append(items, map[string]any{"field_name": name, "field_id": "fld-" + name, "type": 1})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": items, "has_more": false}})
	default:
		http.NotFound(w, r)
	}
}

type workManagementTestServer struct {
	store *repo.Store
	gin   *gin.Engine
	auth  *auth.Client
	users map[string]domain.User
}

func newWorkManagementTestServer(t *testing.T, fake *fakeWorkManagementFeishu) *workManagementTestServer {
	t.Helper()
	t.Setenv("AGENTFLOW_PLATFORM_ADMINS", "demo-owner")
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	for _, name := range []string{"member"} {
		if err := store.PutUser(domain.User{LoginName: name}); err != nil {
			t.Fatal(err)
		}
	}
	bot := domain.NotificationBot{
		ID: "bot-feishu", ProjectID: "platform", Channel: domain.BotChannelFeishu,
		BotID: "cli_test", Name: "飞书应用",
	}
	if err := store.SaveNotificationBot(bot, "app-secret"); err != nil {
		t.Fatal(err)
	}
	feishu := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(feishu.Close)
	client := auth.NewClient(auth.Config{SessionSecret: "work-management-test-secret"})
	d := &deps{repo: store, authClient: client}
	adapter := workmanagement.NewFeishuAdapter(workmanagement.AdapterOptions{
		BaseURL:  feishu.URL,
		Mappings: store,
		Credentials: func(ctx context.Context, botConfigID string) (string, string, error) {
			stored, err := store.NotificationBot(botConfigID)
			if err != nil {
				return "", "", err
			}
			secret, err := store.BotCredential(botConfigID)
			if err != nil {
				return "", "", err
			}
			return stored.BotID, secret, nil
		},
	})
	handler := &workManagementHandlers{deps: d, adapter: adapter}
	g := gin.New()
	handler.register(g)
	users := map[string]domain.User{}
	for _, name := range []string{"demo-owner", "member"} {
		user, ok := store.UserByLoginName(name)
		if !ok {
			t.Fatalf("missing user %s", name)
		}
		users[name] = user
	}
	return &workManagementTestServer{store: store, gin: g, auth: client, users: users}
}

func (s *workManagementTestServer) request(t *testing.T, login, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// platformAdminWrite requires a same-origin browser request or a token.
	req.Header.Set("Origin", "http://"+req.Host)
	if login != "" {
		user := s.users[login]
		token, err := s.auth.SignUser(&auth.User{ID: user.ID, LoginName: user.LoginName, AuthTime: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	s.gin.ServeHTTP(rec, req)
	return rec
}

func decodeWorkManagementResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

const validWorkManagementConnection = `{"name":"飞书需求表","platform":"feishu_bitable","botId":"bot-feishu","baseAppToken":"bascn","tableId":"tbl-1","defaultProjectId":"platform","allowWriteback":false,"enabled":true}`

const validFieldMappings = `{"fieldMappings":[
 {"field":"business_key","externalFieldName":"业务编号"},
 {"field":"title","externalFieldName":"标题"},
 {"field":"status","externalFieldName":"状态"},
 {"field":"description","externalFieldName":"描述"}
]}`

func TestWorkManagementConnectionCRUDAndVerify(t *testing.T) {
	fake := &fakeWorkManagementFeishu{}
	fake.setFields("业务编号", "标题", "状态", "描述")
	s := newWorkManagementTestServer(t, fake)

	if rec := s.request(t, "", http.MethodGet, "/api/work-management/connections", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.request(t, "member", http.MethodPut, "/api/work-management/connections", validWorkManagementConnection); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin write = %d %s", rec.Code, rec.Body.String())
	}

	rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", validWorkManagementConnection)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeWorkManagementResponse(t, rec)
	connection, _ := body["connection"].(map[string]any)
	connectionID, _ := connection["id"].(string)
	if strings.TrimSpace(connectionID) == "" {
		t.Fatalf("generated connection id missing: %+v", body)
	}
	if allow, ok := connection["allowWriteback"].(bool); !ok || allow {
		t.Fatalf("writeback must default off: %+v", connection)
	}

	if rec := s.request(t, "demo-owner", http.MethodGet, "/api/work-management/connections", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), connectionID) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", validFieldMappings); rec.Code != http.StatusOK {
		t.Fatalf("field mappings = %d %s", rec.Code, rec.Body.String())
	}
	rec = s.request(t, "demo-owner", http.MethodGet, "/api/work-management/connections/"+connectionID+"/field-mappings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get field mappings = %d %s", rec.Code, rec.Body.String())
	}
	body = decodeWorkManagementResponse(t, rec)
	mappings, _ := body["fieldMappings"].([]any)
	if len(mappings) != 4 {
		t.Fatalf("field mappings = %+v", body)
	}
	foundID := false
	for _, raw := range mappings {
		item, _ := raw.(map[string]any)
		if item["field"] == "business_key" && item["externalFieldId"] == "fld-业务编号" {
			foundID = true
		}
	}
	if !foundID {
		t.Fatalf("field ids were not resolved: %+v", mappings)
	}

	statuses := `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop","workflowId":"sop-1"},{"externalStatus":"草稿","action":"ignore"}]}`
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/status-mappings", statuses); rec.Code != http.StatusOK {
		t.Fatalf("status mappings = %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.request(t, "demo-owner", http.MethodGet, "/api/work-management/connections/"+connectionID+"/status-mappings", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "start_sop") {
		t.Fatalf("get status mappings = %d %s", rec.Code, rec.Body.String())
	}

	rec = s.request(t, "demo-owner", http.MethodPost, "/api/work-management/connections/"+connectionID+"/verify", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	body = decodeWorkManagementResponse(t, rec)
	readiness, _ := body["readiness"].(map[string]any)
	if readiness["state"] != domain.ManagementReadinessReady {
		t.Fatalf("readiness = %+v", readiness)
	}
	stored, ok := s.store.ManagementConnection(connectionID)
	if !ok || stored.Readiness.State != domain.ManagementReadinessReady {
		t.Fatalf("stored readiness = %+v", stored)
	}

	if rec := s.request(t, "demo-owner", http.MethodDelete, "/api/work-management/connections/"+connectionID, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.request(t, "demo-owner", http.MethodGet, "/api/work-management/connections/"+connectionID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWorkManagementVerifyFailureMarksNotReady(t *testing.T) {
	fake := &fakeWorkManagementFeishu{}
	fake.setFields("业务编号", "标题")
	s := newWorkManagementTestServer(t, fake)

	rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", validWorkManagementConnection)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	connection := decodeWorkManagementResponse(t, rec)["connection"].(map[string]any)
	connectionID := connection["id"].(string)
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", validFieldMappings); rec.Code != http.StatusOK {
		t.Fatalf("field mappings = %d %s", rec.Code, rec.Body.String())
	}

	rec = s.request(t, "demo-owner", http.MethodPost, "/api/work-management/connections/"+connectionID+"/verify", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeWorkManagementResponse(t, rec)
	if message, _ := body["error"].(string); !strings.Contains(message, "状态") {
		t.Fatalf("error = %+v", body)
	}
	stored, _ := s.store.ManagementConnection(connectionID)
	if stored.Readiness.State != domain.ManagementReadinessNotReady || strings.TrimSpace(stored.Readiness.Message) == "" {
		t.Fatalf("stored readiness = %+v", stored.Readiness)
	}
}

func TestWorkManagementRejectsInvalidConfiguration(t *testing.T) {
	fake := &fakeWorkManagementFeishu{}
	fake.setFields("业务编号", "标题", "状态")
	s := newWorkManagementTestServer(t, fake)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"unsupported platform", strings.Replace(validWorkManagementConnection, `"feishu_bitable"`, `"jira"`, 1)},
		{"missing bot", strings.Replace(validWorkManagementConnection, `"bot-feishu"`, `"bot-missing"`, 1)},
		{"missing project", strings.Replace(validWorkManagementConnection, `"defaultProjectId":"platform"`, `"defaultProjectId":"project-missing"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", validWorkManagementConnection)
	connection := decodeWorkManagementResponse(t, rec)["connection"].(map[string]any)
	connectionID := connection["id"].(string)

	invalidFields := `{"fieldMappings":[{"field":"title","externalFieldName":"标题"}]}`
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", invalidFields); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid field mappings = %d %s", rec.Code, rec.Body.String())
	}
	invalidStatus := `{"statusMappings":[{"externalStatus":"开发中","action":"start_sop"}]}`
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/status-mappings", invalidStatus); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status mappings = %d %s", rec.Code, rec.Body.String())
	}
}

const requiredFieldMappings = `{"fieldMappings":[
 {"field":"business_key","externalFieldName":"业务编号"},
 {"field":"title","externalFieldName":"标题"},
 {"field":"status","externalFieldName":"状态"}
]}`

func TestWorkManagementSyncEntryValidatesConnection(t *testing.T) {
	fake := &fakeWorkManagementFeishu{}
	fake.setFields("业务编号", "标题", "状态")
	s := newWorkManagementTestServer(t, fake)

	disabled := strings.Replace(validWorkManagementConnection, `"enabled":true`, `"enabled":false`, 1)
	rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", disabled)
	connection := decodeWorkManagementResponse(t, rec)["connection"].(map[string]any)
	connectionID := connection["id"].(string)
	if rec := s.request(t, "demo-owner", http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", ""); rec.Code != http.StatusConflict {
		t.Fatalf("disabled sync = %d %s", rec.Code, rec.Body.String())
	}

	// The first phase is single-connection: a second connection is rejected.
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", validWorkManagementConnection); rec.Code != http.StatusConflict {
		t.Fatalf("second connection = %d %s", rec.Code, rec.Body.String())
	}

	enabledBody := strings.Replace(validWorkManagementConnection, `{"name"`, `{"id":"`+connectionID+`","name"`, 1)
	rec = s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections", enabledBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.request(t, "demo-owner", http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("sync without mappings = %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.request(t, "demo-owner", http.MethodPut, "/api/work-management/connections/"+connectionID+"/field-mappings", requiredFieldMappings); rec.Code != http.StatusOK {
		t.Fatalf("field mappings = %d %s", rec.Code, rec.Body.String())
	}
	rec = s.request(t, "demo-owner", http.MethodPost, "/api/work-management/connections/"+connectionID+"/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeWorkManagementResponse(t, rec)
	if accepted, _ := body["accepted"].(bool); !accepted {
		t.Fatalf("sync response = %+v", body)
	}
}
