package httpapi

import (
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/gitwork"
)

// hydrateRepositoryServiceGitCredentials merges repository-level service
// credentials into an authenticated runner claim. It runs after the user's
// credentials so a service credential wins for the same host. Secrets are
// hydrated here only and never written back to the task queue.
func (h *runnersHandlers) hydrateRepositoryServiceGitCredentials(task *domain.RunnerTask, runner domain.Runner) bool {
	if task == nil || task.Invocation == nil {
		return false
	}
	repositories := append([]domain.RunnerRepository(nil), task.Invocation.Repositories...)
	if task.Invocation.Repository != nil {
		repositories = append(repositories, *task.Invocation.Repository)
	}
	changed := false
	for _, item := range repositories {
		repository, ok := h.repo.Repository(strings.TrimSpace(item.RepositoryID))
		if !ok || strings.TrimSpace(repository.ServiceCredentialID) == "" {
			continue
		}
		credential, ok := h.repo.RepositoryGitCredential(repository.ServiceCredentialID)
		if !ok {
			continue
		}
		secret, err := h.repo.RepositoryGitCredentialSecret(credential.ID)
		if err != nil || secret == "" {
			continue
		}
		overlay, ok := gitwork.BuildServiceCredentialOverlay(credential, secret, item.CloneURL)
		if !ok {
			continue
		}
		switch {
		case overlay.HTTP != nil:
			if !runner.SupportsGitHTTP() {
				continue
			}
			if strings.HasPrefix(overlay.HTTP.Host, "http://") && !runner.SupportsGitPlainHTTP() {
				continue
			}
			if task.Invocation.GitHTTPCredentials == nil {
				task.Invocation.GitHTTPCredentials = map[string]domain.GitHTTPExecutionCredential{}
			}
			task.Invocation.GitHTTPCredentials[overlay.HTTP.Host] = *overlay.HTTP
			changed = true
		case overlay.SSH:
			if !runner.SupportsGitSSH() || !item.UseSSH || strings.TrimSpace(item.CloneURL) == "" {
				continue
			}
			if task.Invocation.GitSSHKeys == nil {
				task.Invocation.GitSSHKeys = map[string]string{}
			}
			task.Invocation.GitSSHKeys[overlay.SSHURL] = overlay.SSHKey
			changed = true
		}
	}
	return changed
}
