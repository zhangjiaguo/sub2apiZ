// Package admin —— 用户进站消息明文留存管理接口（仅管理员）。
package admin

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserMessageLogHandler 用户消息留存查询接口。
type UserMessageLogHandler struct {
	logService *service.UserMessageLogService
}

// NewUserMessageLogHandler 创建处理器。
func NewUserMessageLogHandler(logService *service.UserMessageLogService) *UserMessageLogHandler {
	return &UserMessageLogHandler{logService: logService}
}

// List 分页查询进站消息（列表项正文为预览）。
// GET /api/v1/admin/user-message-logs
func (h *UserMessageLogHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	filter := &service.UserMessageLogFilter{
		Page:     page,
		PageSize: pageSize,
		Model:    strings.TrimSpace(c.Query("model")),
		Query:    strings.TrimSpace(c.Query("q")),
	}
	if v := strings.TrimSpace(c.Query("user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		filter.UserID = &id
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}
	if v := strings.TrimSpace(c.Query("start_time")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.BadRequest(c, "Invalid start_time, expect RFC3339")
			return
		}
		filter.StartTime = &t
	}
	if v := strings.TrimSpace(c.Query("end_time")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.BadRequest(c, "Invalid end_time, expect RFC3339")
			return
		}
		filter.EndTime = &t
	}
	result, err := h.logService.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Paginated(c, result.Items, result.Total, result.Page, result.PageSize)
}

// Get 查询单条消息全文。
// GET /api/v1/admin/user-message-logs/:id
func (h *UserMessageLogHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid id")
		return
	}
	item, err := h.logService.Get(c.Request.Context(), id)
	if errors.Is(err, service.ErrUserMessageLogNotFound) {
		response.NotFound(c, "消息不存在")
		return
	}
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, item)
}
