package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// OpenAIModelTraceHandler 处理 Codex 降智检测（modeltrace）的 HTTP 请求。
type OpenAIModelTraceHandler struct {
	service *service.CodexModelTraceService
}

// NewOpenAIModelTraceHandler 创建降智检测处理器。
func NewOpenAIModelTraceHandler(service *service.CodexModelTraceService) *OpenAIModelTraceHandler {
	return &OpenAIModelTraceHandler{service: service}
}

// UpdateOpenAIModelTraceSettingsRequest 保存降智检测配置请求。
type UpdateOpenAIModelTraceSettingsRequest struct {
	Models           []string `json:"models"`
	AccountIDs       []int64  `json:"account_ids"`
	BankRepeats      int      `json:"bank_repeats"`
	DetectRepeats    int      `json:"detect_repeats"`
	Concurrency      int      `json:"concurrency"`
	ProbeTimeoutSecs int      `json:"probe_timeout_secs"`
	RequestGapMS     int      `json:"request_gap_ms"`
}

// GetSettings 获取降智检测配置
// GET /api/v1/admin/openai/modeltrace/config
func (h *OpenAIModelTraceHandler) GetSettings(c *gin.Context) {
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

// UpdateSettings 保存降智检测配置
// PUT /api/v1/admin/openai/modeltrace/config
func (h *OpenAIModelTraceHandler) UpdateSettings(c *gin.Context) {
	var req UpdateOpenAIModelTraceSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := service.CodexModelTraceSettings{
		Models:           req.Models,
		AccountIDs:       req.AccountIDs,
		BankRepeats:      req.BankRepeats,
		DetectRepeats:    req.DetectRepeats,
		Concurrency:      req.Concurrency,
		ProbeTimeoutSecs: req.ProbeTimeoutSecs,
		RequestGapMS:     req.RequestGapMS,
	}
	if err := h.service.UpdateSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, settings)
}

// RunOpenAIModelTraceRequest 手动触发请求。
type RunOpenAIModelTraceRequest struct {
	Kind       string   `json:"kind" binding:"required"` // bank | detect
	Models     []string `json:"models,omitempty"`
	AccountIDs []int64  `json:"account_ids,omitempty"`
}

// Run 手动触建库/检测任务
// POST /api/v1/admin/openai/modeltrace/run
func (h *OpenAIModelTraceHandler) Run(c *gin.Context) {
	var req RunOpenAIModelTraceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	runReq := service.CodexModelTraceRunRequest{
		Kind:       req.Kind,
		Models:     req.Models,
		AccountIDs: req.AccountIDs,
	}
	var (
		task *service.CodexModelTraceTaskStatus
		err  error
	)
	switch req.Kind {
	case "bank":
		task, err = h.service.RunBankBuild(c.Request.Context(), runReq)
	case "detect":
		task, err = h.service.RunDetect(c.Request.Context(), runReq)
	default:
		response.BadRequest(c, "kind 必须是 bank 或 detect")
		return
	}
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, task)
}

// Status 任务/指纹库/样本统计
// GET /api/v1/admin/openai/modeltrace/status
func (h *OpenAIModelTraceHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// ListResults 检测结果
// GET /api/v1/admin/openai/modeltrace/results?task_id=&limit=&offset=
func (h *OpenAIModelTraceHandler) ListResults(c *gin.Context) {
	taskID := c.Query("task_id")
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	results, err := h.service.ListResults(c.Request.Context(), taskID, limit, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, results)
}

// ListSamples 建库样本（排查用）
// GET /api/v1/admin/openai/modeltrace/samples?model=&limit=
func (h *OpenAIModelTraceHandler) ListSamples(c *gin.Context) {
	model := c.Query("model")
	limit, _ := strconv.Atoi(c.Query("limit"))
	samples, err := h.service.ListSamples(c.Request.Context(), model, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, samples)
}

// DeleteSamples 清理样本
// DELETE /api/v1/admin/openai/modeltrace/samples?model=
func (h *OpenAIModelTraceHandler) DeleteSamples(c *gin.Context) {
	model := c.Query("model")
	n, err := h.service.DeleteSamples(c.Request.Context(), model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": n})
}
