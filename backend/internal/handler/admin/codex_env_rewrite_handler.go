package admin

import (
	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexEnvRewriteHandler 处理 Codex 环境改写（<environment_context> 时区/日期）的
// HTTP 请求。
type CodexEnvRewriteHandler struct {
	service *service.CodexEnvRewriteService
}

// NewCodexEnvRewriteHandler 创建环境改写处理器。
func NewCodexEnvRewriteHandler(service *service.CodexEnvRewriteService) *CodexEnvRewriteHandler {
	return &CodexEnvRewriteHandler{service: service}
}

// GetSettings 获取环境改写配置
// GET /api/v1/admin/openai/codex-env-rewrite/config
func (h *CodexEnvRewriteHandler) GetSettings(c *gin.Context) {
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

// UpdateSettingsRequest 保存环境改写配置请求。
type UpdateCodexEnvRewriteSettingsRequest struct {
	Enabled          bool              `json:"enabled"`
	Mode             string            `json:"mode"`
	CustomTZ         string            `json:"custom_tz"`
	DefaultTZ        string            `json:"default_tz"`
	EgressCacheHours int               `json:"egress_cache_hours"`
	Overrides        map[string]string `json:"overrides"`
}

// UpdateSettings 保存环境改写配置
// PUT /api/v1/admin/openai/codex-env-rewrite/config
func (h *CodexEnvRewriteHandler) UpdateSettings(c *gin.Context) {
	var req UpdateCodexEnvRewriteSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := service.CodexEnvRewriteSettings{
		Enabled:          req.Enabled,
		Mode:             req.Mode,
		CustomTZ:         req.CustomTZ,
		DefaultTZ:        req.DefaultTZ,
		EgressCacheHours: req.EgressCacheHours,
		Overrides:        req.Overrides,
	}
	if err := h.service.UpdateSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, settings)
}
