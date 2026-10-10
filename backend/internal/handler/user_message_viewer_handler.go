// Package handler —— 用户消息页面（查看者视角，非管理员）。
// 授权来自 user_message_viewers（管理员在页面上授予）：被授权用户只能看
// 自己被授权分组范围内的进站消息与该分组的用量排行。
package handler

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserMessageViewerHandler 用户消息查看者接口（登录即可路由，未授权返回 403）。
type UserMessageViewerHandler struct {
	logService *service.UserMessageLogService
}

// NewUserMessageViewerHandler 创建处理器。
func NewUserMessageViewerHandler(logService *service.UserMessageLogService) *UserMessageViewerHandler {
	return &UserMessageViewerHandler{logService: logService}
}

func (h *UserMessageViewerHandler) subjectUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}

// MyGroups 我的可见分组。
// GET /api/v1/user-message-logs/my-groups
func (h *UserMessageViewerHandler) MyGroups(c *gin.Context) {
	userID, ok := h.subjectUserID(c)
	if !ok {
		return
	}
	groups, err := h.logService.ViewerGroups(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	named, err := h.logService.ViewGroupNames(c.Request.Context(), groups)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, named)
}

// List 查看者视角消息列表（分组范围强制收敛到授权集合）。
// GET /api/v1/user-message-logs
func (h *UserMessageViewerHandler) List(c *gin.Context) {
	userID, ok := h.subjectUserID(c)
	if !ok {
		return
	}
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
	result, err := h.logService.ListForViewer(c.Request.Context(), userID, filter)
	if errors.Is(err, service.ErrUserMessageViewerDenied) {
		response.Forbidden(c, "未授权查看用户消息")
		return
	}
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Paginated(c, result.Items, result.Total, result.Page, result.PageSize)
}

// Get 查看者视角单条全文（必须落在授权分组内）。
// GET /api/v1/user-message-logs/:id
func (h *UserMessageViewerHandler) Get(c *gin.Context) {
	userID, ok := h.subjectUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid id")
		return
	}
	item, err := h.logService.GetForViewer(c.Request.Context(), userID, id)
	if errors.Is(err, service.ErrUserMessageViewerDenied) {
		response.Forbidden(c, "未授权查看该消息")
		return
	}
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

// UsageRanking 查看者视角分组用量排行（分组必须在授权集合内）。
// GET /api/v1/user-message-logs/usage-summary?group_id=&days=
func (h *UserMessageViewerHandler) UsageRanking(c *gin.Context) {
	userID, ok := h.subjectUserID(c)
	if !ok {
		return
	}
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group_id")
		return
	}
	allowed, err := h.logService.ViewerGroups(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	authorized := false
	for _, gid := range allowed {
		if gid == groupID {
			authorized = true
			break
		}
	}
	if !authorized {
		response.Forbidden(c, "未授权查看该分组")
		return
	}
	days := 7
	if v := strings.TrimSpace(c.Query("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	rows, err := h.logService.RankGroupUsage(c.Request.Context(), groupID, days)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"items": rows, "days": days, "group_id": groupID})
}
