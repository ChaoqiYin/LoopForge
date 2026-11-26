package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// recordRunDeliveries writes the Delivery evidence of a successful top-level
// run. A Delivery does not depend on a PR/MR provider: the provider URL is an
// optional detail, so a Git-only repository still completes the flow.
func (e *Engine) recordRunDeliveries(run domain.Run, mrs []domain.DeliveryMR, at time.Time) {
	if e == nil || e.repo == nil || run.IsTemporary() {
		return
	}
	issueID := strings.TrimSpace(run.ExecutionContext.IssueID)
	if issueID == "" || strings.TrimSpace(run.ExecutionContext.ParentRunID) != "" {
		return
	}
	projectID := strings.TrimSpace(run.ExecutionContext.ProjectID)
	if projectID == "" {
		if issue, ok := e.repo.Issue(issueID); ok {
			projectID = issue.ProjectID
		}
	}
	if projectID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	repositories := make([]domain.RepositoryContext, 0, len(run.RepositoryContexts()))
	for _, rc := range run.RepositoryContexts() {
		if rc.AlignOnly {
			// Baseline alignment is not a work-item delivery.
			continue
		}
		repositories = append(repositories, rc)
	}
	if len(repositories) == 0 {
		repositories = []domain.RepositoryContext{{}}
	}
	unspecified := domain.DeliveryCheck{State: domain.DeliveryCheckSkipped, Summary: "运行未上报该检查结果"}
	for index, rc := range repositories {
		repositoryName := rc.Name
		if repositoryName == "" && rc.RepositoryID != "" {
			if repository, ok := e.repo.Repository(rc.RepositoryID); ok {
				repositoryName = repository.Name
			}
		}
		delivery := domain.Delivery{
			ID:             deliveryRecordID(run.ID, rc.RepositoryID, index),
			WorkItemID:     issueID,
			ProjectID:      projectID,
			RepositoryID:   rc.RepositoryID,
			RepositoryName: repositoryName,
			Branch:         rc.WorkBranch,
			CommitSHA:      runDeliveredCommit(run, rc),
			Tests:          unspecified,
			Review:         unspecified,
			Approval:       unspecified,
			State:          domain.DeliveryStateSucceeded,
			CompletedAt:    at.UTC(),
		}
		if mr, ok := deliveryMRForRepository(mrs, rc); ok {
			delivery.ProviderMRURL = mr.URL
		}
		_ = e.repo.PutDelivery(delivery)
	}
}

// deliveryRecordID is deterministic so a replayed finish settles the same
// evidence row instead of creating a duplicate.
func deliveryRecordID(runID, repositoryID string, index int) string {
	key := strings.TrimSpace(repositoryID)
	if key == "" {
		key = fmt.Sprintf("index-%d", index)
	}
	sum := sha256.Sum256([]byte("delivery\x00" + runID + "\x00" + key))
	return "dlv-" + hex.EncodeToString(sum[:])[:24]
}

func deliveryMRForRepository(mrs []domain.DeliveryMR, rc domain.RepositoryContext) (domain.DeliveryMR, bool) {
	for _, mr := range mrs {
		if rc.RepositoryID != "" && mr.RepositoryID == rc.RepositoryID {
			return mr, true
		}
		if rc.GongfengProjectID != "" && strings.EqualFold(strings.TrimSpace(mr.Project), strings.TrimSpace(rc.GongfengProjectID)) {
			return mr, true
		}
	}
	if rc.RepositoryID == "" && rc.GongfengProjectID == "" && len(mrs) > 0 {
		return mrs[0], true
	}
	return domain.DeliveryMR{}, false
}

// runDeliveredCommit returns the last commit a run reported for a repository.
// Telemetry is evidence, not a requirement: an empty value is allowed.
func runDeliveredCommit(run domain.Run, rc domain.RepositoryContext) string {
	for index := len(run.Telemetry) - 1; index >= 0; index-- {
		attributes := run.Telemetry[index].Attributes
		if attributes == nil {
			continue
		}
		if rc.RepositoryID != "" {
			repository, _ := attributes["repositoryId"].(string)
			if strings.TrimSpace(repository) != rc.RepositoryID {
				continue
			}
		} else if rc.WorkBranch != "" {
			branch, _ := attributes["workBranch"].(string)
			if strings.TrimSpace(branch) != rc.WorkBranch {
				continue
			}
		}
		if commit, _ := attributes["commit"].(string); strings.TrimSpace(commit) != "" {
			return strings.TrimSpace(commit)
		}
	}
	return ""
}
