package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Accept-Language 归一：只在客户端发了才改（真实 codex CLI 不发该头，
// 不能替客户端凭空补）。
func TestNormalizeOpenAIAcceptLanguage(t *testing.T) {
	t.Run("中文 locale 归一", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/x", nil)
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		normalizeOpenAIAcceptLanguage(req)
		require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
		require.Len(t, req.Header.Values("Accept-Language"), 1)
	})
	t.Run("未携带不补", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/x", nil)
		normalizeOpenAIAcceptLanguage(req)
		require.Empty(t, req.Header.Get("Accept-Language"))
	})
	t.Run("下划线变体也归一", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/x", nil)
		req.Header["Accept_Language"] = []string{"zh-CN"}
		normalizeOpenAIAcceptLanguage(req)
		require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
		require.Empty(t, req.Header["Accept_Language"])
	})
	t.Run("多值折叠为单值", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/x", nil)
		req.Header.Add("Accept-Language", "ja")
		req.Header.Add("Accept-Language", "fr-CA,fr;q=0.9")
		normalizeOpenAIAcceptLanguage(req)
		require.Equal(t, []string{"en-US,en;q=0.9"}, req.Header.Values("Accept-Language"))
	})
	t.Run("其他头不受影响", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/x", nil)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("User-Agent", "codex_x/1.0")
		normalizeOpenAIAcceptLanguage(req)
		require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
		require.Equal(t, "codex_x/1.0", req.Header.Get("User-Agent"))
	})
}
