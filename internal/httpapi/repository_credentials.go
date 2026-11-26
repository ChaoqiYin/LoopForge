package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/gitwork"
	"agentflow/internal/repo"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// repositoryCredentialsHandlers owns repository-level Git service credentials.
// Responses carry only metadata: the sealed secret is never echoed.
type repositoryCredentialsHandlers struct {
	*deps
}

type repositoryGitCredentialInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

func (h *repositoryCredentialsHandlers) credentials(c *gin.Context) {
	repository, ok := h.repo.Repository(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if !h.canManageProjectRepositories(c, repository.ProjectID) {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
	case http.MethodPut:
		input := repositoryGitCredentialInput{}
		if !decode(c, &input) {
			return
		}
		credentialID := strings.TrimSpace(input.ID)
		if credentialID == "" {
			credentialID = fmt.Sprintf("cred-%d", time.Now().UnixNano())
		}
		kind, host, err := normalizeRepositoryGitCredential(input.Kind, input.Host)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := h.repo.PutRepositoryGitCredential(repository.ID, credentialID, domain.RepositoryGitCredentialInput{
			Name: input.Name, Kind: kind, Host: host, Username: input.Username, Secret: input.Secret,
		}); err != nil {
			respondRepositoryGitCredentialError(c, err)
			return
		}
	case http.MethodDelete:
		credentialID := strings.TrimSpace(c.Param("credId"))
		credential, ok := h.repo.RepositoryGitCredential(credentialID)
		if !ok || credential.RepositoryID != repository.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "仓库凭据不存在"})
			return
		}
		if err := h.repo.DeleteRepositoryGitCredential(credentialID); err != nil {
			respond(c, err)
			return
		}
		h.detachRepositoryServiceCredential(credentialID)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}
	credentials, err := h.repo.RepositoryGitCredentials(repository.ID)
	if err != nil {
		respond(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, credentials)
}

func normalizeRepositoryGitCredential(kind, host string) (string, string, error) {
	kind = strings.TrimSpace(kind)
	host = strings.TrimSpace(host)
	switch kind {
	case domain.RepositoryGitCredentialSSH:
		scope, err := gitwork.SSHHostScope(host)
		if err != nil {
			return "", "", err
		}
		return kind, scope, nil
	case domain.RepositoryGitCredentialHTTPS:
		endpoint, err := domain.ParseGitHTTPEndpoint(host)
		if err != nil {
			return "", "", err
		}
		return kind, endpoint.Scope(), nil
	default:
		return "", "", errors.New("请选择有效的仓库凭据类型")
	}
}

func respondRepositoryGitCredentialError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrRepositoryGitCredentialName):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, repo.ErrRepositoryGitCredentialNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "仓库凭据不存在"})
	default:
		respond(c, err)
	}
}

// detachRepositoryServiceCredential clears references to a deleted credential
// so a repository never points at a missing secret.
func (h *repositoryCredentialsHandlers) detachRepositoryServiceCredential(credentialID string) {
	for _, repository := range h.repo.Repositories() {
		if repository.ServiceCredentialID != credentialID {
			continue
		}
		repository.ServiceCredentialID = ""
		_ = h.repo.PutRepository(repository)
	}
}
