package domain

import (
	"fmt"
	"strings"
	"time"
)

// Repository Git service credential kinds.
const (
	RepositoryGitCredentialSSH   = "ssh"
	RepositoryGitCredentialHTTPS = "https"
)

// RepositoryGitCredential is the safe metadata of a repository-level service
// credential. The secret is sealed with AES-GCM and never returned.
type RepositoryGitCredential struct {
	ID           string    `json:"id"`
	RepositoryID string    `json:"repositoryId"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Host         string    `json:"host"`
	Username     string    `json:"username,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

// RepositoryGitCredentialInput carries a new or replacement secret.
type RepositoryGitCredentialInput struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret"`
}

// Validate checks a service credential before it is sealed.
func (in RepositoryGitCredentialInput) Validate() error {
	name := strings.TrimSpace(in.Name)
	if count := len([]rune(name)); count < 1 || count > 80 {
		return fmt.Errorf("请输入 1 到 80 个字符的凭据名称")
	}
	switch in.Kind {
	case RepositoryGitCredentialSSH:
	case RepositoryGitCredentialHTTPS:
		if strings.TrimSpace(in.Username) == "" {
			return fmt.Errorf("HTTPS 凭据必须填写用户名")
		}
	default:
		return fmt.Errorf("请选择有效的仓库凭据类型")
	}
	if strings.TrimSpace(in.Host) == "" {
		return fmt.Errorf("必须填写 Git 主机")
	}
	if strings.TrimSpace(in.Secret) == "" {
		return fmt.Errorf("必须填写凭据内容")
	}
	return nil
}
