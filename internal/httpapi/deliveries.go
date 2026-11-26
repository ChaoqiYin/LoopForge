package httpapi

import (
	"net/http"
	"strings"

	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

// deliveriesHandlers exposes Delivery evidence read-only. Delivery is
// independent of PR/MR providers; the provider URL is an optional detail.
type deliveriesHandlers struct {
	*deps
}

func (h *deliveriesHandlers) deliveries(c *gin.Context) {
	if c.Request.Method != http.MethodGet {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}
	workItemID := strings.TrimSpace(c.Query("workItemId"))
	projectID := strings.TrimSpace(c.Query("projectId"))
	switch {
	case workItemID != "":
		issue, ok := h.repo.Issue(workItemID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "work item not found"})
			return
		}
		projectID = issue.ProjectID
	case projectID != "":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "workItemId 或 projectId 必须提供一个"})
		return
	}
	if !h.requireProjectWriter(c, projectID) {
		return
	}
	var (
		deliveries []domain.Delivery
		err        error
	)
	if workItemID != "" {
		deliveries, err = h.repo.DeliveriesByWorkItem(workItemID)
	} else {
		deliveries, err = h.repo.DeliveriesByProject(projectID)
	}
	if err != nil {
		respond(c, err)
		return
	}
	if deliveries == nil {
		deliveries = []domain.Delivery{}
	}
	c.JSON(http.StatusOK, deliveries)
}

func (h *deliveriesHandlers) delivery(c *gin.Context) {
	delivery, ok := h.repo.Delivery(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "delivery not found"})
		return
	}
	if !h.requireProjectWriter(c, delivery.ProjectID) {
		return
	}
	c.JSON(http.StatusOK, delivery)
}
