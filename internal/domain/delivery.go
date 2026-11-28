package domain

import (
	"fmt"
	"strings"
	"time"
)

// Delivery is LoopForge's own delivery evidence. It does not depend on a
// PR/MR provider: ProviderMRURL is an optional provider detail. Checks
// (tests, review, approval) are optional until an execution source produces
// one; a missing source stays skipped with a readable reason.

// Delivery check states. A check is optional until the SOP produces one.
const (
	DeliveryCheckPending = "pending"
	DeliveryCheckPassed  = "passed"
	DeliveryCheckFailed  = "failed"
	DeliveryCheckSkipped = "skipped"
)

// DeliveryState is the final delivery lifecycle state.
const (
	DeliveryStateDraft     = "draft"
	DeliveryStateRunning   = "running"
	DeliveryStateSucceeded = "succeeded"
	DeliveryStateFailed    = "failed"
	DeliveryStateCancelled = "cancelled"
)

// DeliveryCheck records one verification dimension (test, review, approval).
type DeliveryCheck struct {
	State     string    `json:"state,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	URL       string    `json:"url,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ValidDeliveryCheckState reports whether state is a supported check state.
func ValidDeliveryCheckState(state string) bool {
	switch state {
	case "", DeliveryCheckPending, DeliveryCheckPassed, DeliveryCheckFailed, DeliveryCheckSkipped:
		return true
	}
	return false
}

// Delivery is one development delivery produced by LoopForge.
type Delivery struct {
	ID             string        `json:"id"`
	WorkItemID     string        `json:"workItemId"`
	ProjectID      string        `json:"projectId"`
	RepositoryID   string        `json:"repositoryId"`
	RepositoryName string        `json:"repositoryName,omitempty"`
	Branch         string        `json:"branch,omitempty"`
	CommitSHA      string        `json:"commitSha,omitempty"`
	ProviderMRURL  string        `json:"providerMrUrl,omitempty"`
	Tests          DeliveryCheck `json:"tests,omitempty"`
	Review         DeliveryCheck `json:"review,omitempty"`
	Approval       DeliveryCheck `json:"approval,omitempty"`
	State          string        `json:"state"`
	CreatedAt      time.Time     `json:"createdAt,omitempty"`
	UpdatedAt      time.Time     `json:"updatedAt,omitempty"`
	CompletedAt    time.Time     `json:"completedAt,omitempty"`
}

// Validate checks the fields required to persist a delivery.
func (d Delivery) Validate() error {
	switch {
	case strings.TrimSpace(d.ID) == "":
		return fmt.Errorf("交付 ID 不能为空")
	case strings.TrimSpace(d.WorkItemID) == "":
		return fmt.Errorf("交付必须关联 Work Item")
	case strings.TrimSpace(d.ProjectID) == "":
		return fmt.Errorf("交付必须关联项目")
	}
	switch d.State {
	case DeliveryStateDraft, DeliveryStateRunning, DeliveryStateSucceeded, DeliveryStateFailed, DeliveryStateCancelled:
	default:
		return fmt.Errorf("交付状态无效：%s", d.State)
	}
	for _, check := range []DeliveryCheck{d.Tests, d.Review, d.Approval} {
		if !ValidDeliveryCheckState(check.State) {
			return fmt.Errorf("交付检查状态无效：%s", check.State)
		}
	}
	return nil
}
