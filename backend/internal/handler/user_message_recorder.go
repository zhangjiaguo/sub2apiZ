// security_audit_helper.go 的配套：进站消息明文留存记录器。
// service ↔ securityaudit 存在依赖方向约束（securityaudit 引 service），协议
// 文本抽取只能留在 handler 层执行；service 只收已解析结果（thunk 后台执行）。
package handler

import (
	"encoding/json"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// userMessageLogService 由装配阶段注入（Start 前一次性写入，运行期只读）。
var userMessageLogService atomic.Value // *service.UserMessageLogService

// SetUserMessageLogService 注入进站消息留存服务（wire 阶段调用）。
func SetUserMessageLogService(svc *service.UserMessageLogService) {
	userMessageLogService.Store(svc)
}

// recordUserMessage 把一条进站请求交给留存服务（解析在后台 goroutine 完成，
// 热路径只做一次 body 拷贝 + 入队，满即丢，绝不阻塞请求）。
func recordUserMessage(c *gin.Context, apiKey *service.APIKey, subject middleware.AuthSubject, protocol, model string, body []byte, stage string) {
	svc, _ := userMessageLogService.Load().(*service.UserMessageLogService)
	if svc == nil || len(body) == 0 {
		return
	}
	request := buildSecurityAuditRequest(c, apiKey, subject, protocol, model, body, stage).Clone()
	svc.Submit(func() *service.UserMessageLog {
		snapshot, err := securityaudit.ExtractPromptSnapshot(request)
		if err != nil {
			return nil // 无用户文本（纯工具/系统请求）等 → 跳过
		}
		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(request.Body, &probe)
		entry := &service.UserMessageLog{
			RequestID:     snapshot.RequestID,
			Username:      snapshot.UsernameSnapshot,
			UserEmail:     snapshot.UserEmailSnapshot,
			APIKeyName:    snapshot.APIKeyNameSnapshot,
			GroupName:     snapshot.GroupName,
			Provider:      snapshot.Provider,
			Endpoint:      snapshot.Endpoint,
			Protocol:      snapshot.Protocol,
			Model:         snapshot.Model,
			Stream:        probe.Stream,
			Stage:         snapshot.Stage,
			Content:       snapshot.FullPrompt,
			ContentLength: snapshot.PromptLength,
		}
		if snapshot.UserID > 0 {
			entry.UserID = snapshot.UserID
		}
		if snapshot.APIKeyID > 0 {
			entry.APIKeyID = snapshot.APIKeyID
		}
		if snapshot.GroupID != nil && *snapshot.GroupID > 0 {
			entry.GroupID = *snapshot.GroupID
		}
		entry.Truncated = snapshot.PromptLength > len([]rune(snapshot.FullPrompt))
		return entry
	})
}
