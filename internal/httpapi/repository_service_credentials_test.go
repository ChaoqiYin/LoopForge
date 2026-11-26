package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"agentflow/internal/domain"
)

func TestRepositoryServiceCredentialAPIHidesSecret(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	if err := f.s.PutRepository(domain.Repository{
		ID: "repo-credentials", ProjectID: "platform", Name: "api",
		Provider: domain.RepositoryProviderHTTPS, CloneURL: "https://git.example.com/team/api.git", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}

	rec := cvBRequest(t, f, http.MethodPut, "/api/repositories/repo-credentials/git-credentials", map[string]any{
		"name": "部署令牌", "kind": "https", "host": "git.example.com", "username": "service", "secret": "service-secret",
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("save credential: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "service-secret") {
		t.Fatalf("credential response leaked the secret: %s", rec.Body.String())
	}
	var list []domain.RepositoryGitCredential
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Name != "部署令牌" || list[0].Host != "git.example.com" {
		t.Fatalf("credential list = %+v, %v", list, err)
	}
	credentialID := list[0].ID

	rec = cvBRequest(t, f, http.MethodGet, "/api/repositories/repo-credentials/git-credentials", nil, true)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "service-secret") {
		t.Fatalf("list credentials: %d %s", rec.Code, rec.Body.String())
	}

	rec = cvBRequest(t, f, http.MethodPut, "/api/repositories", map[string]any{
		"id": "repo-credentials", "projectId": "platform", "name": "api",
		"provider": "https", "cloneUrl": "https://git.example.com/team/api.git",
		"defaultBranch": "main", "serviceCredentialId": credentialID,
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach credential: %d %s", rec.Code, rec.Body.String())
	}
	var stored domain.Repository
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil || stored.ServiceCredentialID != credentialID {
		t.Fatalf("repository credential reference = %+v, %v", stored, err)
	}

	rec = cvBRequest(t, f, http.MethodPut, "/api/repositories", map[string]any{
		"id": "repo-credentials", "projectId": "platform", "name": "api",
		"provider": "https", "cloneUrl": "https://git.example.com/team/api.git",
		"defaultBranch": "main", "serviceCredentialId": "missing-credential",
	}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown service credential must be rejected: %d %s", rec.Code, rec.Body.String())
	}

	rec = cvBRequest(t, f, http.MethodDelete, "/api/repositories/repo-credentials/git-credentials/"+credentialID, nil, true)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "service-secret") {
		t.Fatalf("delete credential: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := f.s.RepositoryGitCredential(credentialID); ok {
		t.Fatal("deleted credential still readable")
	}
}

func TestDeletingRepositoryRemovesServiceCredentials(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	if err := f.s.PutRepository(domain.Repository{
		ID: "repo-cleanup", ProjectID: "platform", Name: "api",
		Provider: domain.RepositoryProviderHTTPS, CloneURL: "https://git.example.com/team/api.git", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PutRepositoryGitCredential("repo-cleanup", "cred-cleanup", domain.RepositoryGitCredentialInput{
		Name: "部署令牌", Kind: domain.RepositoryGitCredentialHTTPS,
		Host: "git.example.com", Username: "service", Secret: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	rec := cvBRequest(t, f, http.MethodDelete, "/api/repositories?id=repo-cleanup", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete repository: %d %s", rec.Code, rec.Body.String())
	}
	if list, err := f.s.RepositoryGitCredentials("repo-cleanup"); err != nil || len(list) != 0 {
		t.Fatalf("credentials after repository deletion = %+v, %v", list, err)
	}
}

func TestRunnerClaimHydratesRepositoryServiceCredentials(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	runnerID, session := registerTestRunner(t, f.h)
	runner, _ := f.s.Runner(runnerID)
	runner.RunnerVersion = domain.RunnerVersionConversationSOPWorkspace
	if err := f.s.PutRunner(runner); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SaveGitHTTPCredential(f.user.ID, "", domain.GitHTTPCredentialSecret{
		GitHTTPCredential: domain.GitHTTPCredential{Host: "git.example.com", Platform: domain.GitPlatformOther},
		Token:             "user-token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutRepository(domain.Repository{
		ID: "repo-runner", ProjectID: "platform", Name: "api",
		Provider: domain.RepositoryProviderHTTPS, CloneURL: "https://git.example.com/team/api.git", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PutRepositoryGitCredential("repo-runner", "cred-runner", domain.RepositoryGitCredentialInput{
		Name: "部署令牌", Kind: domain.RepositoryGitCredentialHTTPS,
		Host: "git.example.com", Username: "service", Secret: "service-token",
	}); err != nil {
		t.Fatal(err)
	}
	repository, _ := f.s.Repository("repo-runner")
	repository.ServiceCredentialID = "cred-runner"
	if err := f.s.PutRepository(repository); err != nil {
		t.Fatal(err)
	}

	task := domain.RunnerTask{ID: "service-credential-task", RunnerID: runnerID, Provider: "mock", Invocation: &domain.RunnerInvocation{
		GitHTTPUserID: f.user.ID,
		Repositories: []domain.RunnerRepository{{
			RepositoryID: "repo-runner", CloneURL: "https://git.example.com/team/api.git",
		}},
	}}
	if err := f.s.PutRunnerTask(task); err != nil {
		t.Fatal(err)
	}
	rec := requestWithHeaders(t, f.h, http.MethodPost, "/api/runners/tasks/claim", map[string]any{
		"availableRuntimes": []string{"mock"}, "noWait": true,
	}, map[string]string{"Authorization": "Bearer " + session})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "user-token") {
		t.Fatalf("claim response leaked the overridden user credential: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential claim must not be cacheable")
	}
	var claimed struct {
		Task domain.RunnerTask `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatal(err)
	}
	got := claimed.Task.Invocation.GitHTTPCredentials["git.example.com"]
	if got.Token != "service-token" || got.GitUsername() != "service" {
		t.Fatalf("service credential was not dispatched to the runner: %+v", got)
	}
	stored, _ := f.s.RunnerTask(task.ID)
	if stored.Invocation.GitHTTPCredentials != nil {
		t.Fatal("hydrated credentials were written back into the queue")
	}
}

func TestRunnerClaimHydratesServiceCredentialWithoutResponsibleUser(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	runnerID, session := registerTestRunner(t, f.h)
	runner, _ := f.s.Runner(runnerID)
	runner.RunnerVersion = domain.RunnerVersionConversationSOPWorkspace
	if err := f.s.PutRunner(runner); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutRepository(domain.Repository{
		ID: "repo-external", ProjectID: "platform", Name: "api",
		Provider: domain.RepositoryProviderHTTPS, CloneURL: "https://git.example.com/team/api.git", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PutRepositoryGitCredential("repo-external", "cred-external", domain.RepositoryGitCredentialInput{
		Name: "外部触发令牌", Kind: domain.RepositoryGitCredentialHTTPS,
		Host: "git.example.com", Username: "service", Secret: "external-token",
	}); err != nil {
		t.Fatal(err)
	}
	repository, _ := f.s.Repository("repo-external")
	repository.ServiceCredentialID = "cred-external"
	if err := f.s.PutRepository(repository); err != nil {
		t.Fatal(err)
	}
	task := domain.RunnerTask{ID: "external-service-credential-task", RunnerID: runnerID, Provider: "mock", Invocation: &domain.RunnerInvocation{
		Repositories: []domain.RunnerRepository{{RepositoryID: "repo-external", CloneURL: "https://git.example.com/team/api.git"}},
	}}
	if err := f.s.PutRunnerTask(task); err != nil {
		t.Fatal(err)
	}
	rec := requestWithHeaders(t, f.h, http.MethodPost, "/api/runners/tasks/claim", map[string]any{
		"availableRuntimes": []string{"mock"}, "noWait": true,
	}, map[string]string{"Authorization": "Bearer " + session})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	var claimed struct {
		Task domain.RunnerTask `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatal(err)
	}
	if got := claimed.Task.Invocation.GitHTTPCredentials["git.example.com"]; got.Token != "external-token" {
		t.Fatalf("external trigger did not receive the service credential: %+v", got)
	}
}
