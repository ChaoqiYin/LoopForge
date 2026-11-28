package gitwork

import (
	"testing"

	"agentflow/internal/domain"
)

func TestRepositoryUsesSSHIsTheSingleTransportDecision(t *testing.T) {
	cases := []struct {
		provider string
		cloneURL string
		want     bool
	}{
		{domain.RepositoryProviderSSH, "https://git.example.com/team/api.git", true},
		{domain.RepositoryProviderHTTPS, "git@git.example.com:team/api.git", true},
		{domain.RepositoryProviderHTTPS, "ssh://git@git.example.com/team/api.git", true},
		{domain.RepositoryProviderHTTPS, "https://git.example.com/team/api.git", false},
		{"", "https://git.example.com/team/api.git", false},
	}
	for _, tc := range cases {
		if got := RepositoryUsesSSH(tc.provider, tc.cloneURL); got != tc.want {
			t.Fatalf("RepositoryUsesSSH(%q, %q) = %v, want %v", tc.provider, tc.cloneURL, got, tc.want)
		}
	}
}

func TestBuildServiceCredentialOverlayHTTPSAndSSH(t *testing.T) {
	https, ok := BuildServiceCredentialOverlay(domain.RepositoryGitCredential{
		ID: "cred-http", Name: "服务账号", Kind: domain.RepositoryGitCredentialHTTPS,
		Host: "https://git.example.com:8443", Username: "svc",
	}, "token", "git@git.example.com:team/api.git")
	if !ok || https.HTTP == nil || https.SSH {
		t.Fatalf("HTTPS overlay = %+v, ok=%v", https, ok)
	}
	if https.HTTP.Host != "git.example.com:8443" || https.HTTP.Username != "svc" || https.HTTP.Token != "token" {
		t.Fatalf("HTTPS credential = %+v", https.HTTP)
	}

	ssh, ok := BuildServiceCredentialOverlay(domain.RepositoryGitCredential{
		ID: "cred-ssh", Name: "部署密钥", Kind: domain.RepositoryGitCredentialSSH, Host: "git.example.com",
	}, "private-key", "git@git.example.com:team/api.git")
	if !ok || ssh.HTTP != nil || !ssh.SSH || ssh.SSHURL != "git@git.example.com:team/api.git" || ssh.SSHKey != "private-key" {
		t.Fatalf("SSH overlay = %+v, ok=%v", ssh, ok)
	}

	if _, ok := BuildServiceCredentialOverlay(domain.RepositoryGitCredential{Kind: domain.RepositoryGitCredentialHTTPS, Host: "not a host"}, "token", ""); ok {
		t.Fatal("invalid HTTPS host must not produce an overlay")
	}
	if _, ok := BuildServiceCredentialOverlay(domain.RepositoryGitCredential{Kind: domain.RepositoryGitCredentialSSH}, "key", ""); ok {
		t.Fatal("SSH overlay requires a clone URL")
	}
	if _, ok := BuildServiceCredentialOverlay(domain.RepositoryGitCredential{Kind: domain.RepositoryGitCredentialSSH}, "", "git@x:y.git"); ok {
		t.Fatal("empty secret must not produce an overlay")
	}
}
