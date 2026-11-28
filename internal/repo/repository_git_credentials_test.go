package repo

import (
	"strings"
	"testing"

	"agentflow/internal/domain"
)

func TestRepositoryGitServiceCredentialSealsSecret(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	input := domain.RepositoryGitCredentialInput{
		Name: "部署密钥", Kind: domain.RepositoryGitCredentialSSH,
		Host: "git.example.com", Secret: "-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	meta, err := s.PutRepositoryGitCredential("repo-1", "cred-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "cred-1" || meta.RepositoryID != "repo-1" || meta.Name != input.Name || meta.Kind != input.Kind || meta.Host != input.Host {
		t.Fatalf("credential metadata = %+v", meta)
	}
	secret, err := s.RepositoryGitCredentialSecret("cred-1")
	if err != nil || secret != input.Secret {
		t.Fatalf("secret round trip = %q, %v", secret, err)
	}
	list, err := s.RepositoryGitCredentials("repo-1")
	if err != nil || len(list) != 1 || list[0].ID != "cred-1" {
		t.Fatalf("repo credentials = %+v, %v", list, err)
	}
	if other, err := s.RepositoryGitCredentials("repo-2"); err != nil || len(other) != 0 {
		t.Fatalf("unrelated repo credentials = %+v, %v", other, err)
	}

	var stored string
	if err := s.DB.Table("repository_git_credentials").Select("secret").Where("id = ?", "cred-1").Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PRIVATE KEY") {
		t.Fatal("secret stored in plaintext")
	}

	input.Secret = "rotated-key"
	if _, err := s.PutRepositoryGitCredential("repo-1", "cred-1", input); err != nil {
		t.Fatal(err)
	}
	if secret, err := s.RepositoryGitCredentialSecret("cred-1"); err != nil || secret != "rotated-key" {
		t.Fatalf("rotated secret = %q, %v", secret, err)
	}

	// The name is unique per repository.
	other := input
	other.Name = input.Name
	if _, err := s.PutRepositoryGitCredential("repo-1", "cred-2", other); err == nil {
		t.Fatal("duplicate credential name in one repository should be rejected")
	}

	if _, err := s.PutRepositoryGitCredential("repo-1", "bad", domain.RepositoryGitCredentialInput{Name: "x", Kind: "pat", Host: "h", Secret: "s"}); err == nil {
		t.Fatal("invalid credential should be rejected")
	}

	if err := s.DeleteRepositoryGitCredential("cred-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.RepositoryGitCredential("cred-1"); ok {
		t.Fatal("deleted credential still readable")
	}
	if _, err := s.RepositoryGitCredentialSecret("cred-1"); err == nil {
		t.Fatal("deleted credential secret should not open")
	}
}

func TestDeleteRepositoryGitCredentialsRemovesOnlyThatRepository(t *testing.T) {
	s, err := OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	for _, item := range []struct{ repoID, credentialID string }{
		{"repo-1", "cred-1"},
		{"repo-1", "cred-2"},
		{"repo-2", "cred-3"},
	} {
		if _, err := s.PutRepositoryGitCredential(item.repoID, item.credentialID, domain.RepositoryGitCredentialInput{
			Name: item.credentialID, Kind: domain.RepositoryGitCredentialSSH,
			Host: "git.example.com", Secret: "key",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteRepositoryGitCredentialsForRepository("repo-1"); err != nil {
		t.Fatal(err)
	}
	if list, err := s.RepositoryGitCredentials("repo-1"); err != nil || len(list) != 0 {
		t.Fatalf("repo-1 credentials = %+v, %v", list, err)
	}
	if list, err := s.RepositoryGitCredentials("repo-2"); err != nil || len(list) != 1 || list[0].ID != "cred-3" {
		t.Fatalf("repo-2 credentials = %+v, %v", list, err)
	}
}
