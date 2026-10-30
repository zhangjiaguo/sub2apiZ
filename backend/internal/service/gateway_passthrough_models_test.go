package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type gatewayModelsDiscoveryStub struct {
	body  []byte
	err   error
	calls []int64
}

func (s *gatewayModelsDiscoveryStub) FetchOpenAIModelsList(_ context.Context, account *Account) (*OpenAIModelsResponse, error) {
	s.calls = append(s.calls, account.ID)
	return &OpenAIModelsResponse{Body: s.body}, s.err
}

func gatewayModelsBody(t *testing.T, ids ...string) []byte {
	t.Helper()
	data := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]string{"id": id})
	}
	body, err := json.Marshal(map[string]any{"data": data})
	require.NoError(t, err)
	return body
}

func TestGetAvailableModelsPassthroughUpstreamCatalogue(t *testing.T) {
	groupID := int64(927)
	account := Account{ID: 418, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"stale-model": "stale-upstream"}},
		Extra:       map[string]any{"openai_passthrough": true}}
	for _, tc := range []struct {
		name string
		body []byte
		err  error
		want []string
	}{
		{"完整保留各类模型及分组前缀", gatewayModelsBody(t, "Vendor/custom-model", "CC/claude-model", "GM/glm-model", "GPT/gpt-model"), nil,
			[]string{"CC/claude-model", "GM/glm-model", "GPT/gpt-model", "Vendor/custom-model"}},
		{"成功的空目录不补默认模型", gatewayModelsBody(t), nil, []string{}},
		{"上游失败使用原有回退", nil, errors.New("upstream unavailable"), nil},
		{"无效响应使用原有回退", []byte(`{"error":"unavailable"}`), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovery := &gatewayModelsDiscoveryStub{body: tc.body, err: tc.err}
			svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
				openAIModelsDiscovery: discovery}
			got := svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI)
			require.Equal(t, tc.want, got)
			require.Equal(t, []int64{account.ID}, discovery.calls)
			require.NotContains(t, got, "stale-model")
		})
	}
}

func TestPassthroughCatalogueMergesOrdinaryMappingsWithoutDefaultPollution(t *testing.T) {
	groupID := int64(82)
	discovery := &gatewayModelsDiscoveryStub{body: gatewayModelsBody(t, "remote/text-model")}
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {
		{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": true}},
		{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"model_mapping": map[string]any{"local-alias": "gpt-upstream"}}},
		{ID: 14, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"model_mapping": map[string]any{"other-platform": "claude-upstream"}}},
	}}}
	svc := &GatewayService{accountRepo: repo, openAIModelsDiscovery: discovery}
	require.Equal(t, []string{"local-alias", "remote/text-model"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
	require.Equal(t, []int64{12}, discovery.calls)
}

func TestPassthroughCatalogueFollowsUpstreamAdditionsAndRemovalsAfterCacheExpiry(t *testing.T) {
	groupID := int64(501)
	discovery := &gatewayModelsDiscoveryStub{body: gatewayModelsBody(t, "remote/old")}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {{
		ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": true},
	}}}}, openAIModelsDiscovery: discovery, modelsListCache: gocache.New(time.Minute, time.Minute), modelsListCacheTTL: time.Minute}
	ctx := context.Background()
	require.Equal(t, []string{"remote/old"}, svc.GetAvailableModels(ctx, &groupID, PlatformOpenAI))
	discovery.body = gatewayModelsBody(t, "custom/new", "remote/new")
	require.Equal(t, []string{"remote/old"}, svc.GetAvailableModels(ctx, &groupID, PlatformOpenAI))
	require.Len(t, discovery.calls, 1)
	svc.modelsListCache.Set(modelsListCacheKey(&groupID, PlatformOpenAI), []string{"remote/old"}, time.Nanosecond)
	time.Sleep(time.Millisecond)
	require.Equal(t, []string{"custom/new", "remote/new"}, svc.GetAvailableModels(ctx, &groupID, PlatformOpenAI))
	require.Len(t, discovery.calls, 2)
	discovery.body = gatewayModelsBody(t, "after/account-edit")
	svc.InvalidateAvailableModelsCache(&groupID, PlatformOpenAI)
	require.Equal(t, []string{"after/account-edit"}, svc.GetAvailableModels(ctx, &groupID, PlatformOpenAI))
}

func TestPassthroughCatalogueRespectsAccountRoutingWhitelist(t *testing.T) {
	groupID := int64(321)
	discovery := &gatewayModelsDiscoveryStub{body: gatewayModelsBody(t, "CC/allowed", "GPT/blocked")}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {{
		ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{"openai_passthrough": true, "routing_models": []any{"CC/allowed"}},
	}}}}, openAIModelsDiscovery: discovery}
	require.Equal(t, []string{"CC/allowed"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

// 上游发现失败时，默认目录也必须服从账号的精确路由资格。
func TestPassthroughCatalogueFallbackRespectsRoutingWhitelist(t *testing.T) {
	groupID := int64(322)
	account := Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{"openai_passthrough": true, "routing_models": []any{"gpt-6.1-sol"}}}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		openAIModelsDiscovery: &gatewayModelsDiscoveryStub{err: errors.New("catalogue unavailable")}}
	require.Equal(t, []string{"gpt-6.1-sol"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

// 单个回退账号不能让别的账号不支持的旧型号重新进入合并目录。
func TestPassthroughCatalogueMixedFallbackRespectsRoutingWhitelist(t *testing.T) {
	groupID := int64(323)
	accounts := []Account{
		{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{"openai_passthrough": true, "routing_models": []any{"CC/allowed"}}},
		{ID: 14, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Extra: map[string]any{"openai_passthrough": true, "routing_models": []any{"gpt-6.1-sol"}}},
	}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: accounts}},
		openAIModelsDiscovery: &gatewayModelsDiscoveryStub{body: gatewayModelsBody(t, "CC/allowed")}}
	require.Equal(t, []string{"CC/allowed", "gpt-6.1-sol"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestMappedCatalogueRespectsRoutingWhitelist(t *testing.T) {
	groupID := int64(324)
	account := Account{ID: 15, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.2": "gpt-5.2", "gpt-6.1-sol": "gpt-6.1-sol"}},
		Extra:       map[string]any{"routing_models": []any{"gpt-6.1-sol"}}}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}}}
	require.Equal(t, []string{"gpt-6.1-sol"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}
