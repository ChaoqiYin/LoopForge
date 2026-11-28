package repo

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow/internal/domain"

	"gorm.io/gorm"
)

// Repository-level Git service credentials let externally triggered runs push
// without a logged-in user. The secret is sealed with AES-256-GCM through the
// platform credential keyring and bound to its repository and credential ID.
// User-level credentials remain an optional override.

var (
	ErrRepositoryGitCredentialNotFound = errors.New("仓库 Git 服务凭据不存在")
	ErrRepositoryGitCredentialName     = errors.New("该仓库已有同名凭据")
)

type repositoryGitCredentialRow struct {
	ID           string     `gorm:"column:id;primaryKey;size:64"`
	RepositoryID string     `gorm:"column:repository_id;size:64;uniqueIndex:uq_repository_git_credential_name,priority:1"`
	Name         string     `gorm:"column:name;size:128;uniqueIndex:uq_repository_git_credential_name,priority:2"`
	Kind         string     `gorm:"column:kind;size:16"`
	Host         string     `gorm:"column:host;size:255"`
	Username     string     `gorm:"column:username;size:128"`
	Secret       string     `gorm:"column:secret;type:text;not null"`
	Created      *time.Time `gorm:"column:created_at"`
	Updated      *time.Time `gorm:"column:updated_at"`
}

func (repositoryGitCredentialRow) TableName() string { return "repository_git_credentials" }

func (row repositoryGitCredentialRow) metadata() domain.RepositoryGitCredential {
	out := domain.RepositoryGitCredential{
		ID: row.ID, RepositoryID: row.RepositoryID, Name: row.Name, Kind: row.Kind,
		Host: row.Host, Username: row.Username,
	}
	if row.Created != nil {
		out.CreatedAt = row.Created.UTC()
	}
	if row.Updated != nil {
		out.UpdatedAt = row.Updated.UTC()
	}
	return out
}

func repositoryGitCredentialAAD(repositoryID, credentialID string) string {
	return "repository-git-credential|" + strings.TrimSpace(repositoryID) + "|" + strings.TrimSpace(credentialID)
}

// PutRepositoryGitCredential creates or replaces one service credential and
// seals its secret. The returned metadata never contains the secret.
func (r *UsersRepo) PutRepositoryGitCredential(repositoryID, credentialID string, in domain.RepositoryGitCredentialInput) (domain.RepositoryGitCredential, error) {
	repositoryID, credentialID = strings.TrimSpace(repositoryID), strings.TrimSpace(credentialID)
	if repositoryID == "" || credentialID == "" {
		return domain.RepositoryGitCredential{}, fmt.Errorf("仓库和凭据 ID 不能为空")
	}
	if err := in.Validate(); err != nil {
		return domain.RepositoryGitCredential{}, err
	}
	keyring := r.credentialKeyring()
	if keyring == nil {
		return domain.RepositoryGitCredential{}, ErrCredentialKeyMissing
	}
	name := strings.TrimSpace(in.Name)
	var conflict repositoryGitCredentialRow
	err := r.DB.Where("repository_id = ? AND name = ? AND id <> ?", repositoryID, name, credentialID).Take(&conflict).Error
	if err == nil {
		return domain.RepositoryGitCredential{}, ErrRepositoryGitCredentialName
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RepositoryGitCredential{}, err
	}

	now := time.Now().UTC()
	created := now
	exists := false
	var existing repositoryGitCredentialRow
	if err := r.DB.Where("id = ?", credentialID).Take(&existing).Error; err == nil {
		if existing.RepositoryID != repositoryID {
			return domain.RepositoryGitCredential{}, ErrRepositoryGitCredentialNotFound
		}
		exists = true
		created = utcValue(existing.Created)
		if created.IsZero() {
			created = now
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RepositoryGitCredential{}, err
	}

	secret, err := keyring.Seal([]byte(in.Secret), repositoryGitCredentialAAD(repositoryID, credentialID))
	if err != nil {
		return domain.RepositoryGitCredential{}, err
	}
	row := repositoryGitCredentialRow{
		ID: credentialID, RepositoryID: repositoryID, Name: name, Kind: in.Kind,
		Host: strings.TrimSpace(in.Host), Username: strings.TrimSpace(in.Username),
		Secret: secret, Created: timePtr(created), Updated: timePtr(now),
	}
	values := map[string]any{
		"id": row.ID, "repository_id": row.RepositoryID, "name": row.Name, "kind": row.Kind,
		"host": row.Host, "username": row.Username, "secret": row.Secret,
		"created_at": row.Created, "updated_at": row.Updated,
	}
	if exists {
		err = r.DB.Model(&repositoryGitCredentialRow{}).Where("id = ?", credentialID).Updates(values).Error
	} else {
		err = r.DB.Table("repository_git_credentials").Create(values).Error
	}
	if err != nil {
		return domain.RepositoryGitCredential{}, err
	}
	return row.metadata(), nil
}

// RepositoryGitCredential reads one credential's safe metadata.
func (r *UsersRepo) RepositoryGitCredential(id string) (domain.RepositoryGitCredential, bool) {
	var row repositoryGitCredentialRow
	if err := r.DB.Where("id = ?", strings.TrimSpace(id)).Take(&row).Error; err != nil {
		return domain.RepositoryGitCredential{}, false
	}
	return row.metadata(), true
}

// RepositoryGitCredentials lists a repository's credentials without secrets.
func (r *UsersRepo) RepositoryGitCredentials(repositoryID string) ([]domain.RepositoryGitCredential, error) {
	var rows []repositoryGitCredentialRow
	if err := r.DB.Omit("secret").Where("repository_id = ?", strings.TrimSpace(repositoryID)).
		Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.RepositoryGitCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.metadata())
	}
	return out, nil
}

// RepositoryGitCredentialSecret opens the sealed secret for a Git operation.
func (r *UsersRepo) RepositoryGitCredentialSecret(id string) (string, error) {
	var row repositoryGitCredentialRow
	if err := r.DB.Where("id = ?", strings.TrimSpace(id)).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrRepositoryGitCredentialNotFound
		}
		return "", err
	}
	keyring := r.credentialKeyring()
	if keyring == nil {
		return "", ErrCredentialKeyMissing
	}
	plain, err := keyring.Open(row.Secret, repositoryGitCredentialAAD(row.RepositoryID, row.ID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DeleteRepositoryGitCredential removes one service credential.
func (r *UsersRepo) DeleteRepositoryGitCredential(id string) error {
	return r.DB.Where("id = ?", strings.TrimSpace(id)).Delete(&repositoryGitCredentialRow{}).Error
}

// DeleteRepositoryGitCredentialsForRepository removes every service credential
// of one repository. Repository deletion calls it so credentials never outlive
// the repository they belong to.
func (r *UsersRepo) DeleteRepositoryGitCredentialsForRepository(repositoryID string) error {
	return r.DB.Where("repository_id = ?", strings.TrimSpace(repositoryID)).
		Delete(&repositoryGitCredentialRow{}).Error
}
