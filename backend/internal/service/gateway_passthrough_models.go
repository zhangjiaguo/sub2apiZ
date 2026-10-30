package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

type openAIModelsDiscovery interface {
	FetchOpenAIModelsList(context.Context, *Account) (*OpenAIModelsResponse, error)
}

// SetOpenAIGatewayService 在启动时接入共享模型发现服务，复用账号级缓存和后台刷新。
func (s *GatewayService) SetOpenAIGatewayService(gateway *OpenAIGatewayService) {
	if s != nil && gateway != nil {
		s.openAIModelsDiscovery = gateway
	}
}

// discoverPassthroughModels 保留上游完整模型名；成功的空目录同样具有权威性。
// 无发现服务或上游失败时返回 false，由原有逻辑提供兼容回退。
func (s *GatewayService) discoverPassthroughModels(ctx context.Context, account *Account) ([]string, bool) {
	if s.openAIModelsDiscovery == nil || account.Type != AccountTypeAPIKey {
		return nil, false
	}
	response, err := s.openAIModelsDiscovery.FetchOpenAIModelsList(ctx, account)
	if err != nil || response == nil {
		slog.WarnContext(ctx, "透传账号模型目录读取失败，使用本地回退", "account_id", account.ID)
		return nil, false
	}
	_, entries, err := modelCatalogEntries(response.Body, "data")
	if err != nil {
		return nil, false
	}
	models := make([]string, 0, len(entries))
	for _, entry := range entries {
		var model struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entry, &model) != nil || strings.TrimSpace(model.ID) == "" {
			return nil, false
		}
		id := strings.TrimSpace(model.ID)
		if !strings.Contains(id, "*") && account.IsModelSupported(id) {
			models = append(models, id)
		}
	}
	return models, true
}
