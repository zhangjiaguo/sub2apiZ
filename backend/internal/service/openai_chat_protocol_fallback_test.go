package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const responsesProtocolDenied = `{"error":{"code":"protocol_not_allowed","message":"This group does not allow OpenAI Responses requests","param":null,"type":"permission_error"}}`

func TestForwardAsChatCompletions_PassthroughProtocolDeniedRetriesOriginalChat(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("unknown=%t/stream=%t", unknown, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"Vendor/mixed-model","messages":[{"role":"user","content":"hello"}],"stream":%t,"vendor_option":"preserve"}`, stream))
				original := bytes.Clone(body)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				account := structuredInputRetryTestAccount()
				account.Extra["openai_passthrough"] = true
				if unknown {
					delete(account.Extra, openai_compat.ExtraKeyResponsesSupported)
				}
				responseBody := `{"id":"chatcmpl_protocol","choices":[{"index":0,"message":{"role":"assistant","content":"PROTOCOL_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
				contentType := "application/json"
				if stream {
					responseBody = "data: {\"id\":\"chatcmpl_protocol\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"PROTOCOL_OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
					contentType = "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(responsesProtocolDenied))},
					{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(responseBody))},
				}}
				svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: upstream}
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 2)
				require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
				require.Equal(t, "/v1/chat/completions", upstream.requests[1].URL.Path)
				require.Contains(t, rec.Body.String(), "PROTOCOL_OK")
				require.Equal(t, original, body)
				require.Equal(t, "/v1/chat/completions", result.UpstreamEndpoint)
				if !unknown {
					require.Equal(t, true, account.Extra[openai_compat.ExtraKeyResponsesSupported], "单个模型拒绝协议不能修改整个账号的能力标记")
				}
				if !stream {
					require.JSONEq(t, string(body), string(upstream.bodies[1]))
				} else {
					require.Contains(t, rec.Body.String(), "[DONE]")
				}
			})
		}
	}
}

func TestForwardAsChatCompletions_ProtocolDeniedRetrySafetyBoundaries(t *testing.T) {
	for _, name := range []string{"ordinary account", "forced responses", "Responses body", "caller canceled", "request canceled", "response committed", "writer committed", "authentication error", "balance error", "truncated error", "partial output", "duplicate code", "contradictory type"} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"Vendor/mixed-model","messages":[{"role":"user","content":"hello"}],"stream":false}`)
			account := structuredInputRetryTestAccount()
			account.Extra["openai_passthrough"] = true
			rejection := responsesProtocolDenied
			switch name {
			case "ordinary account":
				delete(account.Extra, "openai_passthrough")
			case "forced responses":
				account.Extra[openai_compat.ExtraKeyResponsesMode] = "force_responses"
			case "Responses body":
				body = []byte(`{"model":"Vendor/mixed-model","input":"hello","stream":false}`)
			case "authentication error":
				rejection = `{"error":{"code":"invalid_api_key","type":"authentication_error"}}`
			case "balance error":
				rejection = `{"error":{"code":"INSUFFICIENT_BALANCE","type":"permission_error"}}`
			case "truncated error":
				rejection = responsesProtocolDenied[:len(responsesProtocolDenied)-1]
			case "partial output":
				rejection = strings.TrimSuffix(responsesProtocolDenied, "}") + `,"usage":{"input_tokens":5}}`
			case "duplicate code":
				rejection = strings.Replace(responsesProtocolDenied, `"code":"protocol_not_allowed"`, `"code":"authentication_error","code":"protocol_not_allowed"`, 1)
			case "contradictory type":
				rejection = strings.Replace(responsesProtocolDenied, "permission_error", "authentication_error", 1)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(requestCtx)
			reader := &openAIChatFallbackBoundaryReader{Reader: strings.NewReader(rejection), beforeRead: func() {
				switch name {
				case "caller canceled":
					cancel()
				case "request canceled":
					cancelRequest()
				case "response committed":
					MarkResponseCommitted(c)
				case "writer committed":
					c.Writer.WriteHeaderNow()
				}
			}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusForbidden,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(reader)}}
			svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: upstream}
			result, err := svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			require.Error(t, err)
			require.Nil(t, result)
			require.Len(t, upstream.requests, 1)
		})
	}
}
