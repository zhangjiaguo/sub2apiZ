package service

import (
	"strings"
	"sync"
	"testing"

	"log/slog"
)

// warnLongContextUsage：LongContextBillingApplied（含 200K 档模型）或
// totalContext ≥ 272K 兜底时打 usage_long_context_billed WARN，其余静默。
func TestWarnLongContextUsage(t *testing.T) {
	capture := func(t *testing.T, fn func()) string {
		t.Helper()
		var buf syncBuffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		defer slog.SetDefault(previous)
		fn()
		return buf.String()
	}

	strPtr := func(s string) *string { return &s }

	// ① 计费侧已判上浮（200K 档模型、totalContext < 272K）→ 触发。
	logs := capture(t, func() {
		warnLongContextUsage(&UsageLog{
			UserID: 7, APIKeyID: 42, Model: "gpt-5.6-sol",
			SessionID: strPtr("sess-a"), RequestID: "req-1",
			InputTokens: 5_000, CacheReadTokens: 190_000, CacheCreationTokens: 10_000,
			OutputTokens: 900, ActualCost: 1.25,
			LongContextBillingApplied: true,
		})
	})
	if !strings.Contains(logs, "usage_long_context_billed") {
		t.Fatalf("flag-billed request must warn:\n%s", logs)
	}
	for _, want := range []string{"sess-a", "total_context=205000", "model=gpt-5.6-sol"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q:\n%s", want, logs)
		}
	}

	// ② 计费基线缺失（flag=false）但 totalContext ≥ 272K → 兜底触发。
	logs = capture(t, func() {
		warnLongContextUsage(&UsageLog{
			UserID: 1, Model: "gpt-6-astra",
			InputTokens: 3_000, CacheReadTokens: 280_000,
			LongContextBillingApplied: false,
		})
	})
	if !strings.Contains(logs, "usage_long_context_billed") {
		t.Fatalf("272K+ request must warn via floor threshold:\n%s", logs)
	}
	if !strings.Contains(logs, "long_context_billing_applied=false") {
		t.Fatalf("flag must be reported as false:\n%s", logs)
	}

	// ③ 普通请求 → 静默。
	logs = capture(t, func() {
		warnLongContextUsage(&UsageLog{
			UserID: 1, Model: "gpt-6-astra",
			InputTokens: 20_000, CacheReadTokens: 100_000,
		})
	})
	if strings.Contains(logs, "usage_long_context_billed") {
		t.Fatalf("normal request must not warn:\n%s", logs)
	}

	// ④ 边界：totalContext 恰好 272K → 触发；271,999 → 静默。
	logs = capture(t, func() {
		warnLongContextUsage(&UsageLog{UserID: 1, CacheReadTokens: 272_000})
	})
	if !strings.Contains(logs, "usage_long_context_billed") {
		t.Fatalf("exactly 272K must warn:\n%s", logs)
	}
	logs = capture(t, func() {
		warnLongContextUsage(&UsageLog{UserID: 1, CacheReadTokens: 271_999})
	})
	if strings.Contains(logs, "usage_long_context_billed") {
		t.Fatalf("271,999 must not warn:\n%s", logs)
	}

	// ⑤ nil 防御。
	capture(t, func() { warnLongContextUsage(nil) })
}

// syncBuffer 是并发安全的 slog 输出缓冲（TextHandler 可能并发写）。
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
