//go:build integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type compositeLiveHTTPUpstream struct {
	HTTPUpstream
	client *http.Client
	t      *testing.T
}

func (u *compositeLiveHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	response, err := u.client.Do(req)
	if err == nil {
		u.t.Logf("上游端点=%s 状态=%d", req.URL.Path, response.StatusCode)
	}
	return response, err
}

func (u *compositeLiveHTTPUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

// 显式提供运行时凭据才进行真实调用；默认跳过，凭据不写入日志或文件。
func TestCompositeLiveChatRoutes(t *testing.T) {
	key := os.Getenv("SUB2API_COMPOSITE_LIVE_API_KEY")
	baseURL := os.Getenv("SUB2API_COMPOSITE_LIVE_BASE_URL")
	if key == "" || baseURL == "" {
		t.Skip("未提供真实调用测试的运行时凭据")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 120 * time.Second, Transport: transport}
	for _, route := range []struct{ model, expected string }{
		{"GPT/gpt-6.1-sol", "GPT_ROUTE_OK"},
		{"CC/claude-sonnet-4-6", "CC_ROUTE_OK"},
		{"GM/glm-5.3", "GM_ROUTE_OK"},
	} {
		if filter := os.Getenv("SUB2API_COMPOSITE_LIVE_FILTER"); filter != "" && !strings.HasPrefix(route.model, filter+"/") {
			continue
		}
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", route.model, stream), func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"model": route.model, "stream": stream, "max_tokens": 256,
					"messages": []map[string]string{{"role": "user", "content": "连通性测试，请只回复 " + route.expected + "，不要解释。"}}})
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				account := structuredInputRetryTestAccount()
				account.Credentials = map[string]any{"api_key": key, "base_url": baseURL}
				account.Extra["openai_passthrough"] = true
				svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(),
					httpUpstream: &compositeLiveHTTPUpstream{client: client, t: t}}
				ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
				defer cancel()
				result, err := svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, http.StatusOK, rec.Code)
				// 流式输出可能跨多个分片，按协议拼接文本并检查结束标记。
				if stream {
					var text strings.Builder
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if strings.HasPrefix(line, "data:") {
							payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
							text.WriteString(gjson.Get(payload, "choices.0.delta.content").String())
						}
					}
					require.Contains(t, text.String(), route.expected)
					require.Contains(t, rec.Body.String(), "[DONE]")
				} else {
					require.Contains(t, rec.Body.String(), route.expected)
				}
			})
		}
	}
}
