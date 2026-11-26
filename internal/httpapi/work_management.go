package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"agentflow/internal/workmanagement"

	"github.com/gin-gonic/gin"
)

// workManagementHandlers owns Management Connection configuration: CRUD,
// self-check, field/status mappings and the manual-sync entry point. Sync
// orchestration, event intake and reconciliation are wired by later tickets.
type workManagementHandlers struct {
	*deps
	adapter *workmanagement.FeishuAdapter
}

// newFeishuWorkManagementAdapter binds the Feishu adapter to the existing
// encrypted notification-Bot credentials. It does not introduce a second
// credential system: the secret is opened by repo.BotCredential.
func newFeishuWorkManagementAdapter(s *repo.Store) *workmanagement.FeishuAdapter {
	return workmanagement.NewFeishuAdapter(workmanagement.AdapterOptions{
		Mappings: s,
		Credentials: func(_ context.Context, botConfigID string) (string, string, error) {
			bot, err := s.NotificationBot(botConfigID)
			if err != nil {
				return "", "", errors.New("关联的通知机器人不存在")
			}
			if bot.ChannelKind() != domain.BotChannelFeishu {
				return "", "", errors.New("关联的通知机器人不是飞书渠道")
			}
			secret, err := s.BotCredential(botConfigID)
			if err != nil || strings.TrimSpace(secret) == "" {
				return "", "", errors.New("飞书应用凭证不可用，请重新填写 App Secret")
			}
			return bot.BotID, secret, nil
		},
	})
}

func (h *workManagementHandlers) register(g *gin.Engine) {
	g.GET("/api/work-management/connections", h.connections)
	g.PUT("/api/work-management/connections", h.connections)
	g.POST("/api/work-management/connections", h.connections)
	g.GET("/api/work-management/connections/:id", h.connection)
	g.DELETE("/api/work-management/connections/:id", h.connection)
	g.POST("/api/work-management/connections/:id/verify", h.verify)
	g.POST("/api/work-management/connections/:id/sync", h.syncNow)
	g.GET("/api/work-management/connections/:id/field-mappings", h.fieldMappings)
	g.PUT("/api/work-management/connections/:id/field-mappings", h.fieldMappings)
	g.GET("/api/work-management/connections/:id/status-mappings", h.statusMappings)
	g.PUT("/api/work-management/connections/:id/status-mappings", h.statusMappings)
}

type workManagementConnectionInput struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Platform         string `json:"platform"`
	BotID            string `json:"botId"`
	BaseAppToken     string `json:"baseAppToken"`
	TableID          string `json:"tableId"`
	DefaultProjectID string `json:"defaultProjectId"`
	AllowWriteback   bool   `json:"allowWriteback"`
	Enabled          bool   `json:"enabled"`
}

func (h *workManagementHandlers) connections(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		if !h.isPlatformAdmin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以查看外部工作事项连接"})
			return
		}
		connections, err := h.repo.ManagementConnections()
		if err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"connections": connections})
	case http.MethodPut, http.MethodPost:
		if !h.platformAdminWrite(c) {
			return
		}
		h.saveConnection(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (h *workManagementHandlers) saveConnection(c *gin.Context) {
	var input workManagementConnectionInput
	if !decode(c, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Platform = strings.TrimSpace(input.Platform)
	input.BotID = strings.TrimSpace(input.BotID)
	input.BaseAppToken = strings.TrimSpace(input.BaseAppToken)
	input.TableID = strings.TrimSpace(input.TableID)
	input.DefaultProjectID = strings.TrimSpace(input.DefaultProjectID)
	if input.Platform == "" {
		input.Platform = domain.ManagementPlatformFeishuBitable
	}
	if input.Platform != domain.ManagementPlatformFeishuBitable {
		c.JSON(http.StatusBadRequest, gin.H{"error": "首期仅支持飞书多维表格连接"})
		return
	}
	bot, err := h.repo.NotificationBot(input.BotID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "关联的通知机器人不存在"})
		return
	}
	if bot.ChannelKind() != domain.BotChannelFeishu {
		c.JSON(http.StatusBadRequest, gin.H{"error": "关联的通知机器人不是飞书渠道"})
		return
	}
	if _, ok := h.repo.Project(input.DefaultProjectID); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "默认项目不存在"})
		return
	}
	conn := domain.ManagementConnection{
		ID:               strings.TrimSpace(input.ID),
		Name:             input.Name,
		Platform:         input.Platform,
		BotID:            input.BotID,
		BaseAppToken:     input.BaseAppToken,
		TableID:          input.TableID,
		DefaultProjectID: input.DefaultProjectID,
		AllowWriteback:   input.AllowWriteback,
		Enabled:          input.Enabled,
	}
	if conn.ID == "" {
		conn.ID = "wm-" + randomID()
	}
	existing, isUpdate := h.repo.ManagementConnection(conn.ID)
	if isUpdate {
		conn.CreatedAt = existing.CreatedAt
		if sameWorkManagementTarget(existing, conn) {
			conn.Readiness = existing.Readiness
			conn.SyncCursor = existing.SyncCursor
		}
	} else if others, err := h.repo.ManagementConnections(); err != nil {
		respond(c, err)
		return
	} else if len(others) > 0 {
		// First phase is deliberately single-connection: one base, one table.
		c.JSON(http.StatusConflict, gin.H{"error": "首期只支持一个外部工作事项连接，请编辑现有连接"})
		return
	}
	if err := conn.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.PutManagementConnection(conn); err != nil {
		respond(c, err)
		return
	}
	stored, _ := h.repo.ManagementConnection(conn.ID)
	c.JSON(http.StatusOK, gin.H{"connection": stored})
}

func sameWorkManagementTarget(a, b domain.ManagementConnection) bool {
	return strings.TrimSpace(a.BotID) == strings.TrimSpace(b.BotID) &&
		strings.TrimSpace(a.BaseAppToken) == strings.TrimSpace(b.BaseAppToken) &&
		strings.TrimSpace(a.TableID) == strings.TrimSpace(b.TableID)
}

func (h *workManagementHandlers) connection(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	switch c.Request.Method {
	case http.MethodGet:
		if !h.isPlatformAdmin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以查看外部工作事项连接"})
			return
		}
		conn, ok := h.loadConnection(c)
		if !ok {
			return
		}
		fields, err := h.repo.ManagementFieldMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		statuses, err := h.repo.ManagementStatusMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"connection": conn, "fieldMappings": fields, "statusMappings": statuses})
	case http.MethodDelete:
		if !h.platformAdminWrite(c) {
			return
		}
		if _, ok := h.repo.ManagementConnection(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "连接不存在"})
			return
		}
		if err := h.repo.DeleteManagementConnection(id); err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (h *workManagementHandlers) verify(c *gin.Context) {
	if !h.platformAdminWrite(c) {
		return
	}
	conn, ok := h.loadConnection(c)
	if !ok {
		return
	}
	readiness, resolved, ok := h.checkConnection(c, conn)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "readiness": readiness, "fieldMappings": resolved})
}

func (h *workManagementHandlers) syncNow(c *gin.Context) {
	if !h.platformAdminWrite(c) {
		return
	}
	conn, ok := h.loadConnection(c)
	if !ok {
		return
	}
	if !conn.Enabled {
		c.JSON(http.StatusConflict, gin.H{"error": "连接未启用，无法同步"})
		return
	}
	mappings, err := h.repo.ManagementFieldMappings(conn.ID, conn.TableID)
	if err != nil {
		respond(c, err)
		return
	}
	if err := domain.ValidateManagementFieldMappings(mappings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	readiness, _, ok := h.checkConnection(c, conn)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "accepted": true, "readiness": readiness,
		"message": "连接校验通过；同步编排将在后续版本接入",
	})
}

func (h *workManagementHandlers) checkConnection(c *gin.Context, conn domain.ManagementConnection) (domain.ManagementConnectionReadiness, []domain.ManagementFieldMapping, bool) {
	readiness, resolved, err := h.adapter.ValidateAndResolve(c.Request.Context(), conn)
	conn.Readiness = readiness
	if err == nil && len(resolved) > 0 {
		if saveErr := h.repo.PutManagementFieldMappings(resolved); saveErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存字段 ID 缓存失败"})
			return readiness, nil, false
		}
	}
	if putErr := h.repo.PutManagementConnection(conn); putErr != nil {
		respond(c, putErr)
		return readiness, nil, false
	}
	if err != nil {
		c.JSON(workManagementErrorStatus(err), gin.H{"error": workmanagement.UserMessage(err), "readiness": readiness})
		return readiness, nil, false
	}
	return readiness, resolved, true
}

func (h *workManagementHandlers) fieldMappings(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		if !h.isPlatformAdmin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以查看字段映射"})
			return
		}
		conn, ok := h.loadConnection(c)
		if !ok {
			return
		}
		mappings, err := h.repo.ManagementFieldMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"fieldMappings": mappings})
	case http.MethodPut:
		if !h.platformAdminWrite(c) {
			return
		}
		conn, ok := h.loadConnection(c)
		if !ok {
			return
		}
		var input struct {
			FieldMappings []domain.ManagementFieldMapping `json:"fieldMappings"`
		}
		if !decode(c, &input) {
			return
		}
		for i := range input.FieldMappings {
			input.FieldMappings[i].ConnectionID = conn.ID
			input.FieldMappings[i].TableID = conn.TableID
		}
		if err := h.repo.PutManagementFieldMappings(input.FieldMappings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stored, err := h.repo.ManagementFieldMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		response := gin.H{"ok": true, "fieldMappings": stored}
		if resolved, err := h.adapter.ResolveFieldMappings(c.Request.Context(), conn); err != nil {
			response["warning"] = workmanagement.UserMessage(err)
		} else if err := h.repo.PutManagementFieldMappings(resolved); err == nil {
			response["fieldMappings"] = resolved
		}
		c.JSON(http.StatusOK, response)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (h *workManagementHandlers) statusMappings(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		if !h.isPlatformAdmin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以查看状态映射"})
			return
		}
		conn, ok := h.loadConnection(c)
		if !ok {
			return
		}
		mappings, err := h.repo.ManagementStatusMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"statusMappings": mappings})
	case http.MethodPut:
		if !h.platformAdminWrite(c) {
			return
		}
		conn, ok := h.loadConnection(c)
		if !ok {
			return
		}
		var input struct {
			StatusMappings []domain.ManagementStatusMapping `json:"statusMappings"`
		}
		if !decode(c, &input) {
			return
		}
		for i := range input.StatusMappings {
			input.StatusMappings[i].ConnectionID = conn.ID
			input.StatusMappings[i].TableID = conn.TableID
		}
		if err := h.repo.PutManagementStatusMappings(input.StatusMappings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stored, err := h.repo.ManagementStatusMappings(conn.ID, conn.TableID)
		if err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "statusMappings": stored})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (h *workManagementHandlers) loadConnection(c *gin.Context) (domain.ManagementConnection, bool) {
	conn, ok := h.repo.ManagementConnection(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "连接不存在"})
		return domain.ManagementConnection{}, false
	}
	return conn, true
}

func workManagementErrorStatus(err error) int {
	switch {
	case errors.Is(err, workmanagement.ErrCredentialsUnavailable),
		errors.Is(err, workmanagement.ErrFeishuUnavailable),
		errors.Is(err, workmanagement.ErrFeishuRateLimited):
		return http.StatusBadGateway
	case errors.Is(err, workmanagement.ErrFeishuPermission):
		return http.StatusForbidden
	case errors.Is(err, workmanagement.ErrRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, workmanagement.ErrReadOnlyTable),
		errors.Is(err, workmanagement.ErrWritebackDisabled):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}
