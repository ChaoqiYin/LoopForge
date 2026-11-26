package gitwork

import (
	"strings"

	"agentflow/internal/domain"
)

// ServiceCredentialOverlay is the transport-specific effect of one decrypted
// repository-level Git service credential. Exactly one of HTTP or SSHKey is
// set. Callers keep their own capability gate (a Runner may not support SSH or
// plain HTTP) but share the transport decision and credential shape here.
type ServiceCredentialOverlay struct {
	HTTP   *domain.GitHTTPExecutionCredential
	SSH    bool
	SSHURL string
	SSHKey string
}

// RepositoryUsesSSH reports the transport a repository context resolves to.
// It is the single place the platform decides SSH versus HTTP: an explicit SSH
// provider or an SSH clone reference. Legacy project links fall back to HTTPS.
func RepositoryUsesSSH(provider, cloneURL string) bool {
	return provider == domain.RepositoryProviderSSH || IsSSHReference(cloneURL)
}

// BuildServiceCredentialOverlay builds the execution credential for one
// service credential. HTTPS credentials carry the normalized host scope and
// platform; SSH credentials attach the secret to sshURL (the caller decides
// whether that repository actually uses SSH).
func BuildServiceCredentialOverlay(credential domain.RepositoryGitCredential, secret, sshURL string) (ServiceCredentialOverlay, bool) {
	if strings.TrimSpace(secret) == "" {
		return ServiceCredentialOverlay{}, false
	}
	switch credential.Kind {
	case domain.RepositoryGitCredentialHTTPS:
		endpoint, err := domain.ParseGitHTTPEndpoint(credential.Host)
		if err != nil {
			return ServiceCredentialOverlay{}, false
		}
		host := endpoint.Scope()
		platform := domain.GitPlatformOther
		if strings.EqualFold(endpoint.Hostname(), "github.com") {
			platform = domain.GitPlatformGitHub
		}
		return ServiceCredentialOverlay{HTTP: &domain.GitHTTPExecutionCredential{
			GitHTTPCredential: domain.GitHTTPCredential{
				ID: credential.ID, Name: credential.Name, Platform: platform,
				Host: host, Username: credential.Username,
			},
			Token: secret,
		}}, true
	case domain.RepositoryGitCredentialSSH:
		sshURL = strings.TrimSpace(sshURL)
		if sshURL == "" {
			return ServiceCredentialOverlay{}, false
		}
		return ServiceCredentialOverlay{SSH: true, SSHURL: sshURL, SSHKey: secret}, true
	}
	return ServiceCredentialOverlay{}, false
}
