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
// optional detail, so a Git-only repository still completes the flow. Only a
// Formal Work Item bound to a Management Connection produces Delivery; legacy
// Gongfeng/TAPD compatibility rows and local Issues are excluded.
func (e *Engine) recordRunDeliveries(run domain.Run, mrs []domain.DeliveryMR, at time.Time) {
	if e == nil || e.repo == nil || run.IsTemporary() {
		return
	}
	issueID := strings.TrimSpace(run.ExecutionContext.IssueID)
	if issueID == "" || strings.TrimSpace(run.ExecutionContext.ParentRunID) != "" {
		return
	}
	issue, ok := e.repo.Issue(issueID)
	if !ok || !issue.FormalWorkItem() || issue.ExternalRef == nil || issue.ExternalRef.Orphaned {
		return
	}
	projectID := strings.TrimSpace(run.ExecutionContext.ProjectID)
	if projectID == "" {
		projectID = issue.ProjectID
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
	tests, review, approval := deliveryChecks(run)
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
			Tests:          tests,
			Review:         review,
			Approval:       approval,
			State:          domain.DeliveryStateSucceeded,
			CompletedAt:    at.UTC(),
		}
		if mr, ok := deliveryMRForRepository(mrs, rc); ok {
			delivery.ProviderMRURL = mr.URL
		}
		_ = e.repo.PutDelivery(delivery)
		if e.OnDeliveryRecorded != nil {
			// The hook enqueues a delivery-summary writeback. It must never
			// fail or block the Run; the Work Management outbox retries.
			e.OnDeliveryRecorded(delivery)
		}
	}
}

// deliveryChecks derives the structured Tests / Review / Approval evidence
// from the run's artifacts and workflow snapshot. A check with no evidence
// stays skipped and records why, instead of being reported as passed.
func deliveryChecks(run domain.Run) (tests, review, approval domain.DeliveryCheck) {
	now := time.Now().UTC()
	tests = deliveryCheckFromArtifacts(run, []string{"test-report", "tests-report", "测试报告", "test_report"}, "运行未上报测试结果")
	review = deliveryCheckFromArtifacts(run, []string{"review-report", "code-review", "审查报告", "review_report"}, "运行未上报代码审查结果")
	approval = deliveryApprovalCheck(run)
	for _, check := range []*domain.DeliveryCheck{&tests, &review, &approval} {
		if check.State == domain.DeliveryCheckPassed {
			check.UpdatedAt = now
		}
	}
	return tests, review, approval
}

// deliveryCheckFromArtifacts treats a named report artifact as structured
// evidence: the run completed and produced the report. The artifact path is
// kept in the summary so an operator can inspect the source.
func deliveryCheckFromArtifacts(run domain.Run, names []string, missingReason string) domain.DeliveryCheck {
	for _, artifact := range run.Artifacts {
		haystack := strings.ToLower(strings.TrimSpace(artifact.Name + " " + artifact.Path))
		for _, name := range names {
			if !strings.Contains(haystack, strings.ToLower(name)) {
				continue
			}
			summary := "运行产物：" + firstNonEmpty(artifact.Path, artifact.Name)
			return domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: summary}
		}
	}
	return domain.DeliveryCheck{State: domain.DeliveryCheckSkipped, Summary: missingReason}
}

// deliveryApprovalCheck reports an Execution Gate (approval node) that
// completed in this run. It is LoopForge's execution checkpoint, not a
// Business Approval.
func deliveryApprovalCheck(run domain.Run) domain.DeliveryCheck {
	hasGate := false
	for _, node := range run.WorkflowSnapshot.Nodes {
		if node.Type != "approval" {
			continue
		}
		hasGate = true
		if nr := run.Nodes[node.ID]; nr != nil && nr.Status == "succeeded" {
			summary := "执行门禁：" + firstNonEmpty(node.Name, node.ID)
			return domain.DeliveryCheck{State: domain.DeliveryCheckPassed, Summary: summary}
		}
	}
	if !hasGate {
		return domain.DeliveryCheck{State: domain.DeliveryCheckSkipped, Summary: "运行未包含执行门禁"}
	}
	return domain.DeliveryCheck{State: domain.DeliveryCheckSkipped, Summary: "执行门禁未完成"}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
