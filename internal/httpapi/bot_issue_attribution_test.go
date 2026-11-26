package httpapi

import (
	"encoding/json"

	"agentflow/internal/domain"
	"agentflow/internal/engine"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// latestIssueOperation returns the newest operation of the only issue a
// fixture created, so the test asserts on what the platform stored.
func latestIssueOperation(t *testing.T, s *repo.Store, action string) domain.IssueOperation {
	t.Helper()
	var found domain.IssueOperation
	for _, issue := range s.Issues() {
		for _, op := range issue.Operations {
			if op.Action == action && op.At.After(found.At) {
				found = op
			}
		}
	}
	if found.Action == "" {
		t.Fatalf("no %q operation recorded", action)
	}
	return found
}

// A write executed from a chat must name both identities: the platform
// account the write ran as, and the IM member who asked for it. Local issue
// creation is closed, so dispatch is the remaining chat-triggered issue write.
func TestIssueOperationNamesWeComSenderOnConfirmedDraft(t *testing.T) {
	s, h, bot := dispatchAttributionFixture(t, domain.BotChannelWeCom)
	member, _ := s.User(43)
	h.generate = func(ctx context.Context, prompt string, user domain.User) (string, error) {
		session := botTestToolSession(t, user.ID)
		session.commitFinal(AgentTerminalResult{Kind: AgentTerminalAssistant, Assistant: &assistantReply{
			Message: "我会启动这个需求。",
			Drafts:  []assistantDraft{{Tool: "issues_dispatch", Path: map[string]any{"id": "attribution-dispatch"}, Body: json.RawMessage(`{}`)}},
		}})
		return "", nil
	}
	reply, err := h.generateBotReply(context.Background(), bot, domain.BotConversation{SessionID: "bot-session", ChatType: "single"}, nil, domain.BotInteraction{ID: "turn-attribution-confirm", SenderID: "new-wecom-user", Content: "帮我把这个需求跑起来"})
	if err != nil {
		t.Fatal(err)
	}
	m := botDraftLink.FindStringSubmatch(reply)
	if m == nil {
		t.Fatalf("no confirmation link in %q", reply)
	}
	code, done := draftCall(t, h, member, "POST", "/api/bot-drafts/"+m[1]+"/confirm", true)
	if code != 200 || done["status"] != repo.BotDraftExecuted {
		t.Fatalf("confirm = %d %v", code, done)
	}
	op := latestIssueOperation(t, s, "dispatch")
	if op.ActorChannel != domain.BotChannelWeCom || op.ActorSender != "new-wecom-user" {
		t.Fatalf("chat attribution = %q %q", op.ActorChannel, op.ActorSender)
	}
	if op.ActorUserID != 43 || !strings.Contains(op.Actor, "other-user") {
		t.Fatalf("platform actor = %q %d", op.Actor, op.ActorUserID)
	}
}

// The same attribution applies when the Bot writes within the turn instead
// of going through a confirmation card.
func TestIssueOperationNamesWeComSenderOnAutoWrite(t *testing.T) {
	t.Setenv("AGENTFLOW_SESSION_SECRET", "bot-attribution-test-session-secret")
	s, h, bot := dispatchAttributionFixture(t, domain.BotChannelWeCom)
	bot.WriteApproval = domain.BotWriteAuto
	if err := s.SaveNotificationBot(bot, "secret"); err != nil {
		t.Fatal(err)
	}
	bot, _ = s.NotificationBot(bot.ID)
	h.generate = func(ctx context.Context, prompt string, user domain.User) (string, error) {
		session := botTestToolSession(t, user.ID)
		session.commitFinal(AgentTerminalResult{Kind: AgentTerminalAssistant, Assistant: &assistantReply{
			Message: "已启动。",
			Drafts:  []assistantDraft{{Tool: "issues_dispatch", Path: map[string]any{"id": "attribution-dispatch"}, Body: json.RawMessage(`{}`)}},
		}})
		return "", nil
	}
	if _, err := h.generateBotReply(context.Background(), bot, domain.BotConversation{SessionID: "bot-session", ChatType: "single"}, nil, domain.BotInteraction{ID: "turn-attribution-auto", SenderID: "new-wecom-user", Content: "帮我跑个需求"}); err != nil {
		t.Fatal(err)
	}
	op := latestIssueOperation(t, s, "dispatch")
	if op.ActorChannel != domain.BotChannelWeCom || op.ActorSender != "new-wecom-user" {
		t.Fatalf("chat attribution = %q %q", op.ActorChannel, op.ActorSender)
	}
	if op.ActorUserID != 43 {
		t.Fatalf("platform actor = %q %d", op.Actor, op.ActorUserID)
	}
}

// DingTalk reports the sender's name, so the audit entry names a person and
// not only a staff ID.
func TestIssueOperationNamesDingTalkSender(t *testing.T) {
	t.Setenv("AGENTFLOW_SESSION_SECRET", "bot-attribution-dingtalk-secret")
	s, h, bot := dispatchAttributionFixture(t, domain.BotChannelDingTalk)
	bot.WriteApproval = domain.BotWriteAuto
	if err := s.SaveNotificationBot(bot, "secret"); err != nil {
		t.Fatal(err)
	}
	bot, _ = s.NotificationBot(bot.ID)
	h.generate = func(ctx context.Context, prompt string, user domain.User) (string, error) {
		session := botTestToolSession(t, user.ID)
		session.commitFinal(AgentTerminalResult{Kind: AgentTerminalAssistant, Assistant: &assistantReply{
			Message: "已启动。",
			Drafts:  []assistantDraft{{Tool: "issues_dispatch", Path: map[string]any{"id": "attribution-dispatch"}, Body: json.RawMessage(`{}`)}},
		}})
		return "", nil
	}
	item := domain.BotInteraction{ID: "turn-ding", SenderID: "staff-1", SenderName: "张三", Content: "帮我把这个需求跑起来"}
	if _, err := h.generateBotReply(context.Background(), bot, domain.BotConversation{SessionID: "bot-session", ChatType: "single"}, nil, item); err != nil {
		t.Fatal(err)
	}
	op := latestIssueOperation(t, s, "dispatch")
	if op.ActorChannel != domain.BotChannelDingTalk || op.ActorSender != "staff-1" || op.ActorSenderName != "张三" {
		t.Fatalf("chat attribution = %q %q %q", op.ActorChannel, op.ActorSender, op.ActorSenderName)
	}
}

// The issue audit trail is managed by the server on every remaining write
// path, so a caller cannot inject its own operations.
func TestIssueAuditTrailIsServerOwnedOnDispatch(t *testing.T) {
	t.Setenv("AGENTFLOW_SESSION_SECRET", "platform-issue-audit-test-secret")
	s, h, _ := dispatchAttributionFixture(t, domain.BotChannelWeCom)
	member, _ := s.User(43)
	api := New(s, h.engine, tracker.NewService(), "", "")
	req := authedRequest(t, httptest.NewRequest(http.MethodPost, "/api/issues/attribution-dispatch/dispatch", strings.NewReader(`{"operations":[{"action":"create"}]}`)), member)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("dispatch = %d %s", rec.Code, rec.Body.String())
	}
	issue, _ := s.Issue("attribution-dispatch")
	if len(issue.Operations) != 1 || issue.Operations[0].Action != "dispatch" {
		t.Fatalf("operations = %+v", issue.Operations)
	}
	op := issue.Operations[0]
	if op.ActorChannel != "" || op.ActorSender != "" {
		t.Fatalf("unexpected chat attribution %q %q", op.ActorChannel, op.ActorSender)
	}
	if op.ActorUserID != 43 {
		t.Fatalf("platform actor = %q %d", op.Actor, op.ActorUserID)
	}
}

// dispatchAttributionFixture prepares a project, SOP and Work Item that a
// chat write can dispatch. The Work Item is seeded directly because local
// creation is closed; only the external manager creates Work Items now.
func dispatchAttributionFixture(t *testing.T, channel string) (*repo.Store, *assistantHandlers, domain.NotificationBot) {
	t.Helper()
	s, h, bot := botFixture(t)
	bot.WriteApproval = domain.BotWriteConfirm
	if channel == domain.BotChannelDingTalk {
		bot.ID, bot.BotID, bot.Channel, bot.TenantKey, bot.TenantState = "b-dingtalk", "app-dingtalk", domain.BotChannelDingTalk, "tenant", domain.BotTenantLocked
	}
	if err := s.SaveNotificationBot(bot, "secret"); err != nil {
		t.Fatal(err)
	}
	bot, _ = s.NotificationBot(bot.ID)
	if channel == domain.BotChannelDingTalk {
		bindBotTestUser(t, s, bot, "staff-1", 43)
	}
	wf := domain.WorkflowDefinition{
		ID: "attribution-wf", Name: "Attribution", ProjectID: "p", Version: 1,
		Status: "published", SandboxProfileID: repo.TestNativeSandboxID,
		Nodes: []domain.WorkflowNode{{ID: "approval", Name: "Approval", Type: "approval", MaxAttempts: 1}},
	}
	if err := s.PutWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssue(domain.Issue{ID: "attribution-dispatch", ProjectID: "p", Title: "聊天触发的需求", Status: "todo", WorkflowID: wf.ID}); err != nil {
		t.Fatal(err)
	}
	h.engine = engine.New(s, runtime.NewManager(filepath.Join(t.TempDir(), "workspaces")))
	h.api = New(s, h.engine, tracker.NewService(), "", "")
	t.Cleanup(func() {
		for _, run := range s.Runs() {
			_ = h.engine.Cancel(run.ID)
		}
	})
	return s, h, bot
}

// withBotChatActor normalizes the channel and ignores calls without a
// sender, so a missing sender never produces a half-filled audit entry.
func TestBotChatActorContext(t *testing.T) {
	if _, ok := botChatActorFrom(context.Background()); ok {
		t.Fatal("bare context reported a chat actor")
	}
	if _, ok := botChatActorFrom(withBotChatActor(context.Background(), domain.BotChannelWeCom, "  ", "张三")); ok {
		t.Fatal("empty sender reported a chat actor")
	}
	actor, ok := botChatActorFrom(withBotChatActor(context.Background(), "", "wangxx", ""))
	if !ok || actor.Channel != domain.BotChannelWeCom || actor.SenderID != "wangxx" {
		t.Fatalf("actor = %+v ok=%t", actor, ok)
	}
	named, ok := botChatActorFrom(withBotChatActor(context.Background(), domain.BotChannelDingTalk, "  staff-1  ", "  张三  "))
	if !ok || named.Channel != domain.BotChannelDingTalk || named.SenderID != "staff-1" || named.SenderName != "张三" {
		t.Fatalf("named actor = %+v ok=%t", named, ok)
	}
}

// DingTalk is the only channel whose message carries the sender's name. It is
// cached on the binding, so a later action of the same sender that carries no
// name is still attributed to a person.
func TestBotSenderNameIsCachedOnTheBinding(t *testing.T) {
	s, h, bot := botFixture(t)
	bot.ID, bot.BotID, bot.Channel, bot.TenantKey, bot.TenantState = "b-dingtalk", "app-dingtalk", domain.BotChannelDingTalk, "tenant", domain.BotTenantLocked
	if err := s.SaveNotificationBot(bot, "secret"); err != nil {
		t.Fatal(err)
	}
	bot, _ = s.NotificationBot(bot.ID)
	bindBotTestUser(t, s, bot, "staff-1", 43)
	if name := h.botSenderName(bot, "staff-1", ""); name != "" {
		t.Fatalf("unseen sender already named %q", name)
	}
	if name := h.botSenderName(bot, "staff-1", " 张三 "); name != "张三" {
		t.Fatalf("reported name = %q", name)
	}
	// A later card click carries no name and must still resolve it.
	if name := h.botSenderName(bot, "staff-1", ""); name != "张三" {
		t.Fatalf("cached name = %q", name)
	}
	if binding, err := s.BotUserBinding(bot.IdentityScope(), "staff-1"); err != nil || binding.SenderName != "张三" {
		t.Fatalf("binding = %+v err=%v", binding, err)
	}
	// An empty report never clears what is already known.
	if err := s.NoteBotSenderName(bot.IdentityScope(), "staff-1", "  "); err != nil {
		t.Fatal(err)
	}
	if binding, _ := s.BotUserBinding(bot.IdentityScope(), "staff-1"); binding.SenderName != "张三" {
		t.Fatalf("name cleared by an empty report: %q", binding.SenderName)
	}
	// Unbinding drops the cached name with the rest of the identity.
	binding, _ := s.BotUserBinding(bot.IdentityScope(), "staff-1")
	if err := s.RevokeBotUserBinding(binding.ID, 43, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if name := h.botSenderName(bot, "staff-1", ""); name != "" {
		t.Fatalf("name survived revocation: %q", name)
	}
}

// WeCom carries no sender name, so nothing is cached and the audit entry
// keeps naming the sender by userid alone.
func TestWeComSenderHasNoReportedName(t *testing.T) {
	s, h, bot := botFixture(t)
	if name := h.botSenderName(bot, "new-wecom-user", ""); name != "" {
		t.Fatalf("WeCom sender named %q", name)
	}
	if binding, err := s.BotUserBinding(bot.IdentityScope(), "new-wecom-user"); err != nil || binding.SenderName != "" {
		t.Fatalf("binding = %+v err=%v", binding, err)
	}
}
