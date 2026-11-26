package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
)

func TestRepositoryServiceCredentialOverridesUserCredential(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if err := s.SaveGitHTTPCredential(1, "", domain.GitHTTPCredentialSecret{
		GitHTTPCredential: domain.GitHTTPCredential{Host: "git.example.com", Platform: domain.GitPlatformOther},
		Token:             "user-token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRepository(domain.Repository{
		ID: "repo-http", ProjectID: "platform", Name: "api", Provider: domain.RepositoryProviderHTTPS,
		CloneURL: "https://git.example.com/team/api.git", DefaultBranch: "main", ServiceCredentialID: "cred-http",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRepositoryGitCredential("repo-http", "cred-http", domain.RepositoryGitCredentialInput{
		Name: "部署令牌", Kind: domain.RepositoryGitCredentialHTTPS,
		Host: "git.example.com", Username: "service", Secret: "service-token",
	}); err != nil {
		t.Fatal(err)
	}

	e := New(s, runtime.NewManagerWithLimit(t.TempDir(), 1))
	run := domain.Run{
		ID: "run-service-credential",
		Repositories: []domain.RepositoryContext{{
			RepositoryID: "repo-http", Provider: domain.RepositoryProviderHTTPS,
			CloneURL: "https://git.example.com/team/api.git",
		}},
		ExecutionContext: domain.TelemetryContext{ResponsibleUserID: 1},
	}
	credentials := e.resolveRunCredentials(run)
	got := credentials.GitHTTPCredentials["git.example.com"]
	if got.Token != "service-token" || got.GitUsername() != "service" {
		t.Fatalf("service credential did not override the user credential: %+v", got)
	}
}

func TestRepositoryServiceCredentialWithoutResponsibleUser(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if err := s.PutRepository(domain.Repository{
		ID: "repo-ssh", ProjectID: "platform", Name: "api", Provider: domain.RepositoryProviderSSH,
		CloneURL: "git@git.example.com:team/api.git", DefaultBranch: "main", ServiceCredentialID: "cred-ssh",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRepositoryGitCredential("repo-ssh", "cred-ssh", domain.RepositoryGitCredentialInput{
		Name: "部署密钥", Kind: domain.RepositoryGitCredentialSSH,
		Host: "git.example.com", Secret: "service-private-key",
	}); err != nil {
		t.Fatal(err)
	}

	e := New(s, runtime.NewManagerWithLimit(t.TempDir(), 1))
	run := domain.Run{
		ID: "run-service-ssh",
		Repositories: []domain.RepositoryContext{{
			RepositoryID: "repo-ssh", Provider: domain.RepositoryProviderSSH,
			CloneURL: "git@git.example.com:team/api.git",
		}},
	}
	credentials := e.resolveRunCredentials(run)
	if got := credentials.GitSSHKeys["git@git.example.com:team/api.git"]; got != "service-private-key" {
		t.Fatalf("service SSH credential not applied: %q", got)
	}
}

// A successful run creates Delivery evidence even when the repository has no
// PR/MR provider at all.
func TestSuccessfulRunCreatesDeliveryWithoutPullRequest(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	issue := domain.Issue{
		ID: "issue-delivery", ProjectID: "platform", Title: "交付", Status: "todo", Kind: "requirement",
		Source: domain.ManagementPlatformFeishuBitable,
		ExternalRef: &domain.ExternalRef{
			Source: domain.ManagementPlatformFeishuBitable, ConnectionID: "conn-1", TableID: "tbl-1",
			BusinessKey: "REQ-1", RecordID: "rec-1",
		},
	}
	if err := s.PutIssue(issue); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wf := domain.WorkflowDefinition{
		ID: "delivery-flow", Name: "Delivery flow", SandboxProfileID: "test-native",
		Nodes: []domain.WorkflowNode{{ID: "build", Name: "Build", Type: "script", ScriptLanguage: "shell", Script: "true", MaxAttempts: 1}},
	}
	if err := s.PutWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	e := New(s, runtime.NewManagerWithLimit(t.TempDir(), 1))
	run, err := e.StartWithRepositoriesContext(wf.ID, "交付", []domain.RepositoryContext{{
		RepositoryID: "repo-local", Name: "api", Provider: domain.RepositoryProviderHTTPS,
		LocalPath: source, BaseBranch: "main", DefaultBranch: "main", WorkBranch: "work/issue-delivery",
	}}, domain.TelemetryContext{IssueID: issue.ID, ProjectID: "platform", ResponsibleUserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, run.ID, "succeeded")

	var deliveries []domain.Delivery
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		deliveries, err = s.DeliveriesByWorkItem(issue.ID)
		if err != nil || len(deliveries) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	delivery := deliveries[0]
	if delivery.WorkItemID != issue.ID || delivery.ProjectID != "platform" ||
		delivery.RepositoryID != "repo-local" || delivery.RepositoryName != "api" ||
		delivery.Branch != "work/issue-delivery" || delivery.State != domain.DeliveryStateSucceeded ||
		delivery.ProviderMRURL != "" || delivery.CompletedAt.IsZero() {
		t.Fatalf("delivery evidence = %+v", delivery)
	}
	for name, check := range map[string]domain.DeliveryCheck{
		"tests": delivery.Tests, "review": delivery.Review, "approval": delivery.Approval,
	} {
		if check.State != domain.DeliveryCheckSkipped || strings.TrimSpace(check.Summary) == "" {
			t.Fatalf("%s check = %+v, want skipped with a reason", name, check)
		}
	}
}

// Delivery is only produced for a Formal Work Item bound to a Management
// Connection. A local Issue or a legacy Gongfeng/TAPD row is excluded.
func TestNonFormalRunDoesNotCreateDelivery(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	issues := []domain.Issue{
		{ID: "local-issue", ProjectID: "platform", Title: "本地", Status: "todo", Kind: "requirement"},
		{ID: "legacy-gongfeng", ProjectID: "platform", Title: "工蜂", Status: "todo", Kind: "issue",
			Source: "gongfeng", ExternalRef: &domain.ExternalRef{Source: "gongfeng", ExternalID: "9", ExternalIID: 9}},
		{ID: "legacy-tapd", ProjectID: "platform", Title: "TAPD", Status: "todo", Kind: "requirement",
			Source: "tapd", ExternalRef: &domain.ExternalRef{Source: "tapd", ExternalID: "1001"}},
	}
	for _, issue := range issues {
		if err := s.PutIssue(issue); err != nil {
			t.Fatal(err)
		}
	}
	e := &Engine{repo: s}
	for _, issue := range issues {
		e.recordRunDeliveries(domain.Run{
			ID: "run-" + issue.ID,
			Repositories: []domain.RepositoryContext{{
				RepositoryID: "repo-local", Name: "api", WorkBranch: "work/x",
			}},
			ExecutionContext: domain.TelemetryContext{IssueID: issue.ID, ProjectID: "platform"},
		}, nil, time.Now().UTC())
		deliveries, err := s.DeliveriesByWorkItem(issue.ID)
		if err != nil || len(deliveries) != 0 {
			t.Fatalf("%s produced deliveries = %+v, %v", issue.ID, deliveries, err)
		}
	}
}

// Structured Delivery checks come from run artifacts and the workflow gate,
// not from the run merely succeeding.
func TestRecordRunDeliveriesDerivesStructuredChecks(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	issue := domain.Issue{
		ID: "wi-checks", ProjectID: "platform", Title: "交付", Status: "todo", Kind: "requirement",
		Source: domain.ManagementPlatformFeishuBitable,
		ExternalRef: &domain.ExternalRef{
			Source: domain.ManagementPlatformFeishuBitable, ConnectionID: "conn-1", TableID: "tbl-1",
			BusinessKey: "REQ-CHECKS", RecordID: "rec-checks",
		},
	}
	if err := s.PutIssue(issue); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{
		ID:           "run-checks",
		Repositories: []domain.RepositoryContext{{RepositoryID: "repo", Name: "api", WorkBranch: "work/checks"}},
		ExecutionContext: domain.TelemetryContext{
			IssueID: issue.ID, ProjectID: "platform",
		},
		WorkflowSnapshot: domain.WorkflowDefinition{Nodes: []domain.WorkflowNode{
			{ID: "gate", Name: "验收", Type: "approval"},
		}},
		Nodes: map[string]*domain.NodeRun{"gate": {NodeID: "gate", Status: "succeeded"}},
		Artifacts: []domain.Artifact{
			{Name: "test-report.md", Path: "test-report.md"},
			{Name: "review-report.md", Path: "review-report.md"},
		},
	}
	(&Engine{repo: s}).recordRunDeliveries(run, nil, time.Now().UTC())

	deliveries, err := s.DeliveriesByWorkItem(issue.ID)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	delivery := deliveries[0]
	for name, check := range map[string]domain.DeliveryCheck{
		"tests": delivery.Tests, "review": delivery.Review, "approval": delivery.Approval,
	} {
		if check.State != domain.DeliveryCheckPassed || strings.TrimSpace(check.Summary) == "" {
			t.Fatalf("%s check = %+v, want passed with evidence", name, check)
		}
	}
	if !strings.Contains(delivery.Tests.Summary, "test-report.md") {
		t.Fatalf("tests summary = %q", delivery.Tests.Summary)
	}
	if !strings.Contains(delivery.Review.Summary, "review-report.md") {
		t.Fatalf("review summary = %q", delivery.Review.Summary)
	}
	if !strings.Contains(delivery.Approval.Summary, "验收") {
		t.Fatalf("approval summary = %q", delivery.Approval.Summary)
	}
}

func TestResolveRunCredentialsKeepsUserCredentialWhenRepositoryHasNoServiceCredential(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.SaveGitHTTPCredential(1, "", domain.GitHTTPCredentialSecret{
		GitHTTPCredential: domain.GitHTTPCredential{Host: "git.example.com", Platform: domain.GitPlatformOther},
		Token:             "user-token",
	}); err != nil {
		t.Fatal(err)
	}
	e := New(s, runtime.NewManagerWithLimit(t.TempDir(), 1))
	run := domain.Run{
		ID:               "run-user-credential",
		Repositories:     []domain.RepositoryContext{{RepositoryID: "repo-no-service", Provider: domain.RepositoryProviderHTTPS, CloneURL: "https://git.example.com/team/api.git"}},
		ExecutionContext: domain.TelemetryContext{ResponsibleUserID: 1},
	}
	credentials := e.resolveRunCredentials(run)
	if got := credentials.GitHTTPCredentials["git.example.com"].Token; got != "user-token" {
		t.Fatalf("user credential = %q", got)
	}
}

func TestRecordRunDeliveriesLinksProviderMRDetail(t *testing.T) {
	t.Parallel()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.PutIssue(domain.Issue{ID: "issue-mr-detail", ProjectID: "platform", Title: "交付", Status: "todo", Kind: "requirement",
		Source: domain.ManagementPlatformFeishuBitable,
		ExternalRef: &domain.ExternalRef{
			Source: domain.ManagementPlatformFeishuBitable, ConnectionID: "conn-1", TableID: "tbl-1",
			BusinessKey: "REQ-MR", RecordID: "rec-mr",
		}}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{repo: s}
	run := domain.Run{
		ID: "run-mr-detail",
		Repositories: []domain.RepositoryContext{{
			RepositoryID: "repo-mr", Name: "api", GongfengProjectID: "g/api", WorkBranch: "work/mr",
		}},
		ExecutionContext: domain.TelemetryContext{IssueID: "issue-mr-detail", ProjectID: "platform"},
	}
	mrURL := "https://git.example.com/g/api/-/merge_requests/7"
	e.recordRunDeliveries(run, []domain.DeliveryMR{{
		RepositoryID: "repo-mr", Project: "g/api", IID: 7, URL: mrURL, State: "opened",
	}}, time.Now().UTC())

	deliveries, err := s.DeliveriesByWorkItem("issue-mr-detail")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	if deliveries[0].ProviderMRURL != mrURL {
		t.Fatalf("provider MR detail not linked: %+v", deliveries[0])
	}
}
