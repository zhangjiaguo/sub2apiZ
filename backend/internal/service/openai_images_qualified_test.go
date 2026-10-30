//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 验证集成上游的完整模型 ID 能经过解析、资格筛选和真实请求构造，且不会被改写。
func TestCompatibleImagesQualifiedProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"GPT/gpt-image-2", "GPT/gpt-image-2.5-flare", "GM/qwen-image", "GM/qwen-image-edit", "qwen-image", "qwen-image-edit", "custom/gemini-3.1-flash-image"} {
		t.Run(model, func(t *testing.T) {
			endpoint, contentType := openAIImagesGenerationsEndpoint, "application/json"
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","vendor_option":"preserved"}`, model))
			if strings.Contains(model, "edit") {
				endpoint = openAIImagesEditsEndpoint
				var buf bytes.Buffer
				writer := multipart.NewWriter(&buf)
				require.NoError(t, writer.WriteField("model", model))
				require.NoError(t, writer.WriteField("prompt", "edit"))
				require.NoError(t, writer.WriteField("vendor_option", "preserved"))
				part, err := writer.CreateFormFile("image", "fixture.png")
				require.NoError(t, err)
				_, err = part.Write([]byte("unchanged-fixture-bytes"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body, contentType = buf.Bytes(), writer.FormDataContentType()
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", contentType)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapabilityForModel(model))
			for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
				require.False(t, (&Account{Platform: PlatformOpenAI, Type: typ}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
			}
			result, err := svc.ForwardImages(context.Background(), c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://compatible.example/v1"}}, body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, model, result.UpstreamModel)
			require.Equal(t, "https://compatible.example"+endpoint, upstream.lastReq.URL.String())
			if strings.Contains(model, "edit") {
				req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(upstream.lastBody))
				req.Header.Set("Content-Type", upstream.lastReq.Header.Get("Content-Type"))
				require.NoError(t, req.ParseMultipartForm(1<<20))
				require.Equal(t, model, req.FormValue("model"))
				require.Equal(t, "preserved", req.FormValue("vendor_option"))
				file, _, err := req.FormFile("image")
				require.NoError(t, err)
				defer file.Close()
				data, err := io.ReadAll(file)
				require.NoError(t, err)
				require.Equal(t, "unchanged-fixture-bytes", string(data))
			} else {
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "preserved", gjson.GetBytes(upstream.lastBody, "vendor_option").String())
			}
		})
	}
}
