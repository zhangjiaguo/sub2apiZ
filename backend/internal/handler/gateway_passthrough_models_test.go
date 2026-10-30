package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passthroughModelsUpstream struct {
	service.HTTPUpstream
	calls int
}

func (u *passthroughModelsUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
		`{"object":"list","data":[{"id":"GPT/gpt-6.1-sol"},{"id":"CC/claude-sonnet-4-6"},{"id":"GM/glm-5.3"}]}`,
	))}, nil
}

func (u *passthroughModelsUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestGatewayModelsPublishesPassthroughUpstreamCatalogue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID int64 = 730
	repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{groupID: {{
		ID: 177, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://integrated.example/v1", "api_key": "test-key"},
		Extra:       map[string]any{"openai_passthrough": true},
	}}}}
	upstream := &passthroughModelsUpstream{}
	cfg := &config.Config{}
	openAI := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := NewGatewayHandler(newGatewayModelsHandlerForTest(repo).gatewayService, openAI,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
	group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI}
	for _, allowlist := range []bool{false, true} {
		group.ModelAllowlist = service.GroupModelAllowlist{Enabled: allowlist, Models: []string{"CC/*"}}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: group})
		h.Models(c)
		require.Equal(t, http.StatusOK, w.Code)
		var got gatewayModelsResponseForTest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		want := []string{"CC/claude-sonnet-4-6", "GM/glm-5.3", "GPT/gpt-6.1-sol"}
		if allowlist {
			want = []string{"CC/claude-sonnet-4-6"}
		}
		require.Equal(t, want, modelIDsForTest(got.Data))
	}
	require.Equal(t, 1, upstream.calls, "同一账号的目录应复用缓存")
}
