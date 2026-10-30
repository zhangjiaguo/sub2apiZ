package apicompat

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestOfficialCacheUsageRoundTrip 防止缓存TTL和百炼写入别名在协议转换后丢失。
func TestOfficialCacheUsageRoundTrip(t *testing.T) {
	var original ResponsesUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":400,"cache_write_tokens_details":{"ttl_30m_tokens":150,"ttl_12h_tokens":250}}}`), &original))
	chat := chatUsageFromResponsesUsage(&original)
	require.NotNil(t, chat.PromptTokensDetails)
	require.Equal(t, 250, chat.PromptTokensDetails.CacheWriteTokensDetails.TTL12hTokens)
	require.NotSame(t, original.InputTokensDetails.CacheWriteTokensDetails, chat.PromptTokensDetails.CacheWriteTokensDetails)
	roundTrip := ChatUsageToResponsesUsage(chat)
	require.Equal(t, 400, roundTrip.CacheCreationInputTokens)
	require.Equal(t, 250, roundTrip.InputTokensDetails.CacheWriteTokensDetails.TTL12hTokens)
	require.NotSame(t, chat.PromptTokensDetails.CacheWriteTokensDetails, roundTrip.InputTokensDetails.CacheWriteTokensDetails)
	encoded, err := json.Marshal(roundTrip)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"ttl_12h_tokens":250`)
}

// TestOfficialCacheCreationAliasPresence 验证百炼别名参与计费，同时不覆盖权威字段的显式零值。
func TestOfficialCacheCreationAliasPresence(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		expected   int
	}{
		{"百炼嵌套别名", `{"prompt_tokens_details":{"cache_creation_input_tokens":6}}`, 6},
		{"输入嵌套别名", `{"input_tokens_details":{"cache_creation_input_tokens":6}}`, 6},
		{"显式零值优先", `{"input_tokens_details":{"cache_write_tokens":0,"cache_creation_input_tokens":6}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage ResponsesUsage
			require.NoError(t, json.Unmarshal([]byte(tc.wire), &usage))
			require.Equal(t, tc.expected, usage.CacheCreationInputTokens)
		})
	}
	chat := &ChatUsage{PromptTokens: 100, PromptTokensDetails: &ChatTokenDetails{CacheCreationInputTokens: 6}}
	responses := ChatUsageToResponsesUsage(chat)
	require.Equal(t, 6, responses.CacheCreationInputTokens)
	require.Equal(t, 6, chatUsageFromResponsesUsage(responses).PromptTokensDetails.CacheCreationInputTokens)
}
