package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 打票探测顺带采集降智样本：完整 SSE + 足量数字才落库；限频 1 次/账号/小时。
func TestMaybeRecordModelTraceSample(t *testing.T) {
	svc := NewOpenAITicketGrabService(nil, nil, nil, nil, nil)
	var recorded []*CodexModelTraceSample
	svc.SetModelTraceSampleSink(func(ctx context.Context, sample *CodexModelTraceSample) error {
		recorded = append(recorded, sample)
		return nil
	})

	numbers := make([]string, 300)
	for i := range numbers {
		numbers[i] = "17"
	}
	numberText := strings.Join(numbers, ",")
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + numberText + "\"}\n\n" +
		"data: {\"type\":\"response.completed\"}\n\n"

	svc.maybeRecordModelTraceSample(133, "gpt-6-astra", 300, []byte(sse), 5432)
	require.Len(t, recorded, 1)
	require.Equal(t, "gpt-6-astra", recorded[0].Model)
	require.Equal(t, int64(133), recorded[0].AccountID)
	require.Equal(t, 300, recorded[0].ExpectedCount)
	require.Len(t, recorded[0].Numbers, 300)
	require.Equal(t, 5432, recorded[0].LatencyMS)

	// 立即再采：限频跳过。
	svc.maybeRecordModelTraceSample(133, "gpt-6-astra", 300, []byte(sse), 100)
	require.Len(t, recorded, 1)

	// 另一账号不受影响。
	svc.maybeRecordModelTraceSample(213, "gpt-6-astra", 300, []byte(sse), 100)
	require.Len(t, recorded, 2)

	// 流未完成：不落库。
	incomplete := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"1 2 3\"}\n\n"
	svc.maybeRecordModelTraceSample(244, "gpt-6-astra", 300, []byte(incomplete), 100)
	require.Len(t, recorded, 2)

	// 数字不足：不落库。
	thin := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"1 2 3\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
	svc.maybeRecordModelTraceSample(245, "gpt-6-astra", 300, []byte(thin), 100)
	require.Len(t, recorded, 2)

	// 未注入 sink：静默不落库。
	bare := NewOpenAITicketGrabService(nil, nil, nil, nil, nil)
	bare.maybeRecordModelTraceSample(1, "m", 300, []byte(sse), 1)
}

// 限频窗口过后可再采（回拨时钟模拟）。
func TestMaybeRecordModelTraceSampleRateWindowExpiry(t *testing.T) {
	svc := NewOpenAITicketGrabService(nil, nil, nil, nil, nil)
	count := 0
	svc.SetModelTraceSampleSink(func(ctx context.Context, sample *CodexModelTraceSample) error {
		count++
		return nil
	})
	numbers := make([]string, 300)
	for i := range numbers {
		numbers[i] = "42"
	}
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Join(numbers, " ") + "\"}\n\n" +
		"data: {\"type\":\"response.completed\"}\n\n"
	svc.maybeRecordModelTraceSample(1, "m", 300, []byte(sse), 1)
	require.Equal(t, 1, count)
	// 回拨上次采样时间到窗口外。
	svc.sampleMu.Lock()
	svc.sampleLastAt[1] = svc.sampleLastAt[1].Add(-openAITicketProbeSampleInterval - 1)
	svc.sampleMu.Unlock()
	svc.maybeRecordModelTraceSample(1, "m", 300, []byte(sse), 1)
	require.Equal(t, 2, count)
}
