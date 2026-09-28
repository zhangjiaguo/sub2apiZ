package service

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
)

// ---- 测试桩 ----

// codexModelTraceRepoStub 内存版降智检测仓库。
type codexModelTraceRepoStub struct {
	mu      sync.Mutex
	samples []*CodexModelTraceSample
	nextID  int64
	bank    string
	bankAt  time.Time
	results []*CodexModelTraceResult
	nextRes int64
}

func (r *codexModelTraceRepoStub) InsertSample(ctx context.Context, s *CodexModelTraceSample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	s.ID, s.CreatedAt = r.nextID, time.Now()
	cp := *s
	r.samples = append(r.samples, &cp)
	return nil
}

func (r *codexModelTraceRepoStub) ListSamples(ctx context.Context, model string, limit int) ([]*CodexModelTraceSample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*CodexModelTraceSample
	for _, s := range r.samples {
		if model == "" || s.Model == model {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *codexModelTraceRepoStub) DeleteSamples(ctx context.Context, model string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []*CodexModelTraceSample
	var n int64
	for _, s := range r.samples {
		if model == "" || s.Model == model {
			n++
		} else {
			kept = append(kept, s)
		}
	}
	r.samples = kept
	return n, nil
}

func (r *codexModelTraceRepoStub) SaveBank(ctx context.Context, bankJSON string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bank, r.bankAt = bankJSON, time.Now()
	return nil
}

func (r *codexModelTraceRepoStub) GetBank(ctx context.Context) (string, time.Time, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bank == "" {
		return "", time.Time{}, false, nil
	}
	return r.bank, r.bankAt, true, nil
}

func (r *codexModelTraceRepoStub) InsertResult(ctx context.Context, res *CodexModelTraceResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextRes++
	res.ID, res.CreatedAt = r.nextRes, time.Now()
	cp := *res
	r.results = append(r.results, &cp)
	return nil
}

func (r *codexModelTraceRepoStub) ListResults(ctx context.Context, taskID string, limit, offset int) ([]*CodexModelTraceResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*CodexModelTraceResult
	for i := len(r.results) - 1; i >= 0; i-- {
		res := r.results[i]
		if taskID == "" || res.TaskID == taskID {
			out = append(out, res)
		}
	}
	return out, nil
}

// codexModelTraceAccountRepoStub 只实现 GetByID（嵌入接口满足其余方法）。
type codexModelTraceAccountRepoStub struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *codexModelTraceAccountRepoStub) GetByID(ctx context.Context, id int64) (*Account, error) {
	if acc, ok := r.accounts[id]; ok {
		return acc, nil
	}
	return nil, context.DeadlineExceeded
}

// waitModelTraceTask 等任务结束。
func waitModelTraceTask(t *testing.T, svc *CodexModelTraceService) *CodexModelTraceTaskStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		task := svc.snapshotTask()
		if task != nil && task.Finished {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("modeltrace task did not finish in time")
	return nil
}

// mtTestGenerators 每模型一个可区分的合成分布。
func mtTestGenerators() map[string]func(*rand.Rand, int) []int {
	return map[string]func(*rand.Rand, int) []int{
		"mt-low": func(r *rand.Rand, n int) []int {
			out := make([]int, n)
			for i := range out {
				if r.Intn(10) < 8 {
					out[i] = 1 + r.Intn(90)
				} else {
					out[i] = 1 + r.Intn(355)
				}
			}
			return out
		},
		"mt-high": func(r *rand.Rand, n int) []int {
			out := make([]int, n)
			for i := range out {
				if r.Intn(10) < 8 {
					out[i] = 260 + r.Intn(96)
				} else {
					out[i] = 1 + r.Intn(355)
				}
			}
			return out
		},
		"mt-mid": func(r *rand.Rand, n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = 130 + r.Intn(96)
			}
			return out
		},
	}
}

func mtNewService(t *testing.T, accounts map[int64]*Account) (*CodexModelTraceService, *codexModelTraceRepoStub) {
	t.Helper()
	repo := &codexModelTraceRepoStub{}
	settingRepo := newCodexEnvTestRepo()
	accountRepo := &codexModelTraceAccountRepoStub{accounts: accounts}
	svc := NewCodexModelTraceService(repo, accountRepo, nil, settingRepo, nil)
	return svc, repo
}

func TestCodexModelTraceSettingsValidate(t *testing.T) {
	base := DefaultCodexModelTraceSettings()
	if err := base.Validate(); err != nil {
		t.Fatalf("default should validate: %v", err)
	}
	bad := base
	bad.Models = nil
	if err := bad.Validate(); err == nil {
		t.Fatal("empty models must fail")
	}
	bad = base
	bad.Models = []string{"a", "a"}
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate models must fail")
	}
	bad = base
	bad.BankRepeats = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("bank_repeats=0 must fail")
	}
	bad = base
	bad.DetectRepeats = 99
	if err := bad.Validate(); err == nil {
		t.Fatal("detect_repeats=99 must fail")
	}
	bad = base
	bad.Concurrency = 9
	if err := bad.Validate(); err == nil {
		t.Fatal("concurrency=9 must fail")
	}
	bad = base
	bad.ProbeTimeoutSecs = 5
	if err := bad.Validate(); err == nil {
		t.Fatal("probe_timeout=5 must fail")
	}
	bad = base
	bad.AccountIDs = []int64{0}
	if err := bad.Validate(); err == nil {
		t.Fatal("account id 0 must fail")
	}
}

func TestCodexModelTraceSettingsRoundTrip(t *testing.T) {
	repo := newCodexEnvTestRepo()
	svc := NewCodexModelTraceService(nil, nil, nil, repo, nil)
	ctx := context.Background()

	cfg := DefaultCodexModelTraceSettings()
	cfg.AccountIDs = []int64{244, 245}
	cfg.BankRepeats = 10
	if err := svc.UpdateSettings(ctx, cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := svc.GetSettings(ctx)
	if len(got.AccountIDs) != 2 || got.BankRepeats != 10 {
		t.Fatalf("cache not refreshed: %+v", got)
	}

	// 新实例从 DB 读回。
	svc2 := NewCodexModelTraceService(nil, nil, nil, repo, nil)
	got2 := svc2.GetSettings(ctx)
	if len(got2.AccountIDs) != 2 || got2.Models[0] != "gpt-5.4" {
		t.Fatalf("persisted settings lost: %+v", got2)
	}

	// 非法配置拒绝。
	bad := cfg
	bad.Models = nil
	if err := svc.UpdateSettings(ctx, bad); err == nil {
		t.Fatal("invalid settings must be rejected")
	}
}

func TestParseModelTraceSSE(t *testing.T) {
	// delta 累加 + completed。
	stream := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"17, 42,"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":" 355"}`,
		`data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"17, 42, 355"}]}]}}`,
		`data: [DONE]`,
	}, "\n")
	got := parseModelTraceSSE([]byte(stream))
	if !got.Completed || got.Failed != "" {
		t.Fatalf("completed=%v failed=%q", got.Completed, got.Failed)
	}
	if got.Text != "17, 42, 355" {
		t.Fatalf("text=%q", got.Text)
	}

	// 无 delta 时兜底从 completed 的 output 提取。
	fallback := `data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"1 2 3"}]}]}}`
	got = parseModelTraceSSE([]byte(fallback))
	if !got.Completed || got.Text != "1 2 3" {
		t.Fatalf("fallback text=%q completed=%v", got.Text, got.Completed)
	}

	// failed。
	got = parseModelTraceSSE([]byte(`data: {"type":"response.failed","response":{"error":{"message":"model overloaded"}}}`))
	if got.Failed == "" || got.Completed {
		t.Fatalf("failed=%q completed=%v", got.Failed, got.Completed)
	}

	// 裸 error。
	got = parseModelTraceSSE([]byte(`data: {"type":"error","error":{"message":"bad request"}}`))
	if got.Failed == "" {
		t.Fatalf("error not captured: %+v", got)
	}

	// 只有 delta 无 completed → 调用方按 stream_incomplete 处理。
	got = parseModelTraceSSE([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"12\"}\n"))
	if got.Completed || got.Text != "12" {
		t.Fatalf("incomplete stream: %+v", got)
	}

	// 空/噪声行安全。
	got = parseModelTraceSSE([]byte("\r\n: keepalive\n\nnot-data\n"))
	if got.Completed || got.Failed != "" || got.Text != "" {
		t.Fatalf("noise stream: %+v", got)
	}
}

func TestBuildCodexModelTraceRequestBody(t *testing.T) {
	identity := &openAITicketProbeIdentity{
		installationID: "11111111-1111-4111-8111-111111111111",
		sessionID:      "22222222-2222-4222-8222-222222222222",
		threadID:       "33333333-3333-4333-8333-333333333333",
		turnID:         "44444444-4444-4444-8444-444444444444",
		windowID:       "33333333-3333-4333-8333-333333333333:0",
		turnStartedAt:  1700000000000,
	}
	body := buildCodexModelTraceRequestBody("gpt-6-astra", "请生成 300 个 1 到 355（含端点）的整数", identity)
	s := string(body)
	// codex 字段序：model 首位。
	if !strings.HasPrefix(s, `{"model":"gpt-6-astra","instructions":`) {
		t.Fatalf("field order wrong: %.80s", s)
	}
	parsed := gjson.ParseBytes(body)
	if parsed.Get("model").String() != "gpt-6-astra" {
		t.Fatal("model mismatch")
	}
	if text := parsed.Get("input.0.content").String(); !strings.Contains(text, "1 到 355") {
		t.Fatalf("prompt not in input: %q", text)
	}
	if parsed.Get("input.0.role").String() != "user" {
		t.Fatalf("input role: %s", parsed.Get("input.0.role").String())
	}
	if !parsed.Get("stream").Bool() || parsed.Get("store").Bool() {
		t.Fatal("stream/store wrong")
	}
	if parsed.Get("prompt_cache_key").String() != identity.sessionID {
		t.Fatal("prompt_cache_key mismatch")
	}
	if parsed.Get("client_metadata.session_id").String() != identity.sessionID {
		t.Fatal("client_metadata mismatch")
	}
}

func TestResolveCodexModelTraceIdentity(t *testing.T) {
	account := &Account{ID: 77}
	a := resolveCodexModelTraceIdentity(account)
	b := resolveCodexModelTraceIdentity(account)
	if a.threadID != b.threadID || a.sessionID != b.sessionID || a.installationID != b.installationID {
		t.Fatal("identity must be stable per account")
	}
	if a.turnID == b.turnID {
		t.Fatal("turn id must rotate")
	}
	ticket := resolveOpenAITicketProbeIdentity(account)
	if a.threadID == ticket.threadID {
		t.Fatal("modeltrace thread must not collide with ticket probe thread")
	}
	other := resolveCodexModelTraceIdentity(&Account{ID: 78})
	if a.threadID == other.threadID {
		t.Fatal("different accounts must derive different threads")
	}
}

// ---- 端到端（注入探测） ----

func TestCodexModelTraceBankBuildAndDetect(t *testing.T) {
	accounts := map[int64]*Account{
		244: {ID: 244, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
		245: {ID: 245, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
	}
	svc, repo := mtNewService(t, accounts)

	gens := mtTestGenerators()
	models := []string{"mt-low", "mt-high", "mt-mid"}
	r := rand.New(rand.NewSource(99))
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		n := challenge.ExpectedCount
		return codexModelTraceProbeOutcome{numbers: gens[model](r, n), expected: n, latencyMS: 120}
	}

	settings := DefaultCodexModelTraceSettings()
	settings.Models = models
	settings.AccountIDs = []int64{244, 245}
	settings.BankRepeats = 10
	settings.DetectRepeats = 3
	settings.Concurrency = 2
	settings.RequestGapMS = 0
	if err := svc.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}

	// 建库。
	task, err := svc.RunBankBuild(context.Background(), CodexModelTraceRunRequest{Kind: "bank"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitModelTraceTask(t, svc)
	if final.LastError != "" {
		t.Fatalf("bank build failed: %s", final.LastError)
	}
	if final.Total != len(models)*10 || final.Done != final.Total {
		t.Fatalf("task total/done=%d/%d", final.Total, final.Done)
	}
	if repo.bank == "" || !gjson.Valid(repo.bank) {
		t.Fatal("bank not saved / invalid")
	}
	if got := len(gjson.Get(repo.bank, "profiles").Array()); got != 3 {
		t.Fatalf("bank profiles=%d", got)
	}
	samples, _ := repo.ListSamples(context.Background(), "", 0)
	if len(samples) != 30 {
		t.Fatalf("samples=%d want 30", len(samples))
	}
	bankResults, _ := repo.ListResults(context.Background(), task.ID, 0, 0)
	if len(bankResults) != 3 {
		t.Fatalf("bank results=%d", len(bankResults))
	}
	for _, res := range bankResults {
		if res.Verdict != "ok" || res.ValidRuns != 10 {
			t.Fatalf("bank result wrong: %+v", res)
		}
	}

	// 诚实检测：每模型判回自己。
	detectTask, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect"})
	if err != nil {
		t.Fatal(err)
	}
	final = waitModelTraceTask(t, svc)
	if final.LastError != "" {
		t.Fatalf("detect failed: %s", final.LastError)
	}
	results, _ := repo.ListResults(context.Background(), detectTask.ID, 0, 0)
	if len(results) != 6 { // 2 账号 × 3 模型
		t.Fatalf("detect results=%d", len(results))
	}
	for _, res := range results {
		if !res.Match || res.Verdict != res.Model {
			t.Fatalf("honest detect mismatch: %+v", res)
		}
		if res.ValidRuns != 3 || res.Probability < 0.6 {
			t.Fatalf("weak honest verdict: %+v", res)
		}
	}

	// 换模检测：请求 mt-low 实际服务 mt-high 分布 → verdict=mt-high, match=false。
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		n := challenge.ExpectedCount
		serve := model
		if model == "mt-low" {
			serve = "mt-high"
		}
		return codexModelTraceProbeOutcome{numbers: gens[serve](r, n), expected: n, latencyMS: 100}
	}
	subTask, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect", Models: []string{"mt-low"}, AccountIDs: []int64{244}})
	if err != nil {
		t.Fatal(err)
	}
	waitModelTraceTask(t, svc)
	subResults, _ := repo.ListResults(context.Background(), subTask.ID, 0, 0)
	if len(subResults) != 1 {
		t.Fatalf("sub results=%d", len(subResults))
	}
	res := subResults[0]
	if res.Verdict != "mt-high" || res.Match {
		t.Fatalf("substitution not detected: %+v", res)
	}
}

func TestCodexModelTraceDetectAbortsAccountOn429(t *testing.T) {
	accounts := map[int64]*Account{
		244: {ID: 244, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
	}
	svc, repo := mtNewService(t, accounts)
	gens := mtTestGenerators()
	r := rand.New(rand.NewSource(5))
	// 阶段一：诚实探测建库（无熔断）。
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		n := challenge.ExpectedCount
		return codexModelTraceProbeOutcome{numbers: gens[model](r, n), expected: n, latencyMS: 50}
	}

	settings := DefaultCodexModelTraceSettings()
	settings.Models = []string{"mt-low", "mt-high", "mt-mid"}
	settings.AccountIDs = []int64{244}
	settings.BankRepeats = 10
	settings.DetectRepeats = 5
	settings.RequestGapMS = 0
	_ = svc.UpdateSettings(context.Background(), settings)

	if _, err := svc.RunBankBuild(context.Background(), CodexModelTraceRunRequest{Kind: "bank"}); err != nil {
		t.Fatal(err)
	}
	if task := waitModelTraceTask(t, svc); task.LastError != "" {
		t.Fatalf("bank build failed: %s", task.LastError)
	}
	samples, _ := repo.ListSamples(context.Background(), "", 0)
	if len(samples) != 30 {
		t.Fatalf("samples=%d want 30", len(samples))
	}

	// 阶段二：检测任务中第 2 次探测返回 429 → 该账号剩余探测全部跳过。
	call := 0
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		call++
		if call == 2 {
			return codexModelTraceProbeOutcome{reason: "http_429", status: 429, accountAbort: true, expected: challenge.ExpectedCount}
		}
		n := challenge.ExpectedCount
		return codexModelTraceProbeOutcome{numbers: gens[model](r, n), expected: n, latencyMS: 50}
	}
	task, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect", Models: []string{"mt-low"}, AccountIDs: []int64{244}})
	if err != nil {
		t.Fatal(err)
	}
	waitModelTraceTask(t, svc)
	results, _ := repo.ListResults(context.Background(), task.ID, 0, 0)
	if len(results) != 1 {
		t.Fatalf("results=%d", len(results))
	}
	res := results[0]
	if res.ValidRuns != 1 {
		t.Fatalf("valid_runs=%d（熔断应只剩 1 个有效回答）", res.ValidRuns)
	}
	// 失败只计真实探测（1 次 429），跳过的 3 次由熔断说明解释。
	if res.Failures != 1 || !strings.Contains(res.Reasons, "熔断") || !strings.Contains(res.Reasons, "剩余 3 次") {
		t.Fatalf("abort not accounted: %+v", res)
	}
}

func TestCodexModelTraceTaskBusy(t *testing.T) {
	accounts := map[int64]*Account{
		244: {ID: 244, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
	}
	svc, _ := mtNewService(t, accounts)

	block := make(chan struct{})
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		<-block
		return codexModelTraceProbeOutcome{reason: "blocked"}
	}
	settings := DefaultCodexModelTraceSettings()
	settings.Models = []string{"m"}
	settings.AccountIDs = []int64{244}
	settings.BankRepeats = 2
	_ = svc.UpdateSettings(context.Background(), settings)

	if _, err := svc.RunBankBuild(context.Background(), CodexModelTraceRunRequest{Kind: "bank"}); err != nil {
		t.Fatal(err)
	}
	// 任务占用中第二个任务被拒。
	if _, err := svc.RunBankBuild(context.Background(), CodexModelTraceRunRequest{Kind: "bank"}); err == nil {
		t.Fatal("second task must be rejected while one is running")
	}
	if _, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect"}); err == nil {
		t.Fatal("detect must be rejected while bank task is running")
	}
	close(block)
	waitModelTraceTask(t, svc)
	// 结束后任务槽释放（建库会因样本不足失败——只验证槽位可复用）。
	if _, err := svc.RunBankBuild(context.Background(), CodexModelTraceRunRequest{Kind: "bank"}); err != nil {
		t.Fatalf("task slot not released: %v", err)
	}
	waitModelTraceTask(t, svc)
}

// 稳健库未覆盖 + 自建库缺失 → 逐对记 no_bank（任务本身不再失败）。
func TestCodexModelTraceDetectModelNotInAnyBank(t *testing.T) {
	accounts := map[int64]*Account{
		244: {ID: 244, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
	}
	svc, repo := mtNewService(t, accounts)
	settings := DefaultCodexModelTraceSettings()
	settings.Models = []string{"mt-unknown"}
	settings.AccountIDs = []int64{244}
	_ = svc.UpdateSettings(context.Background(), settings)

	if _, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect"}); err != nil {
		t.Fatal(err)
	}
	task := waitModelTraceTask(t, svc)
	if task.LastError != "" {
		t.Fatalf("unexpected task error: %q", task.LastError)
	}
	results, _ := repo.ListResults(context.Background(), task.ID, 0, 0)
	if len(results) != 1 {
		t.Fatalf("results=%d", len(results))
	}
	res := results[0]
	if res.Verdict != "no_bank" || res.Match {
		t.Fatalf("expected no_bank: %+v", res)
	}
	if !strings.Contains(res.Reasons, "稳健库") || !strings.Contains(res.Reasons, "自建库") {
		t.Fatalf("reasons=%q", res.Reasons)
	}
}

// mtRobustSample 从稳健库模型直方图抽 n 个数字（打散后截断，边际分布对齐）。
func mtRobustSample(counts []int, n int, r *rand.Rand) []int {
	pool := make([]int, 0, len(counts)*36)
	for idx, c := range counts {
		for j := 0; j < c; j++ {
			pool = append(pool, idx+1)
		}
	}
	r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) > n {
		pool = pool[:n]
	}
	return pool
}

// 稳健库检测：默认模型走内嵌稳健库（上游模板挑战），直方图重构样本判回自己。
func TestCodexModelTraceDetectRobustBank(t *testing.T) {
	accounts := map[int64]*Account{
		244: {ID: 244, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
	}
	svc, repo := mtNewService(t, accounts)

	bank, err := modeltrace.EmbeddedRobustBank()
	if err != nil {
		t.Fatal(err)
	}
	counts, ok := bank.ModelCounts("gpt-6-astra")
	if !ok {
		t.Fatal("gpt-6-astra not in robust bank")
	}
	r := rand.New(rand.NewSource(7))
	// 挑战应来自上游模板生成器（与稳健库采集环境对齐）。
	seenPrompts := map[string]bool{}
	svc.probeFn = func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome {
		seenPrompts[challenge.Prompt] = true
		return codexModelTraceProbeOutcome{numbers: mtRobustSample(counts, challenge.ExpectedCount, r), expected: challenge.ExpectedCount, latencyMS: 90}
	}

	settings := DefaultCodexModelTraceSettings()
	settings.Models = []string{"gpt-6-astra"}
	settings.AccountIDs = []int64{244}
	settings.DetectRepeats = 3
	settings.RequestGapMS = 0
	_ = svc.UpdateSettings(context.Background(), settings)

	task, err := svc.RunDetect(context.Background(), CodexModelTraceRunRequest{Kind: "detect"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitModelTraceTask(t, svc)
	if final.LastError != "" {
		t.Fatalf("detect failed: %s", final.LastError)
	}
	results, _ := repo.ListResults(context.Background(), task.ID, 0, 0)
	if len(results) != 1 {
		t.Fatalf("results=%d", len(results))
	}
	res := results[0]
	if res.Verdict != "gpt-6-astra" || !res.Match {
		t.Fatalf("robust detect mismatch: %+v", res)
	}
	if res.ValidRuns != 3 || res.Probability < 0.6 {
		t.Fatalf("weak robust verdict: %+v", res)
	}
	if !strings.Contains(res.Reasons, "库=robust") {
		t.Fatalf("bank kind missing: %q", res.Reasons)
	}
	for prompt := range seenPrompts {
		if !strings.Contains(prompt, "355") {
			t.Fatalf("challenge prompt not upstream template: %q", prompt)
		}
	}
}

func TestCodexModelTraceStatusAndSamples(t *testing.T) {
	accounts := map[int64]*Account{244: {ID: 244}}
	svc, repo := mtNewService(t, accounts)
	ctx := context.Background()

	// 空库 status。
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status["task"] != nil {
		t.Fatalf("task should be nil, got %v", status["task"])
	}
	if _, exists := status["bank"]; exists {
		t.Fatal("bank should be absent")
	}

	// 存库后 status 带指纹库与样本统计。
	raw := `{"version":1,"alpha":0.5,"ordered_weight":0.25,"calibration":{"1":2.5},"profiles":[{"model":"m1","samples":9,"marginal":[1],"ordered_centroid":[1],"ordered_variance":[1]}]}`
	if err := repo.SaveBank(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSample(ctx, &CodexModelTraceSample{Model: "m1", Numbers: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	status, err = svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bank, _ := status["bank"].(map[string]any)
	if bank == nil {
		t.Fatal("bank missing in status")
	}
	if got := len(bank["models"].([]map[string]any)); got != 1 {
		t.Fatalf("bank models=%d", got)
	}
	counts := status["sample_counts"].(map[string]int)
	if counts["m1"] != 1 {
		t.Fatalf("sample counts=%v", counts)
	}

	// DeleteSamples / ListSamples。
	n, err := svc.DeleteSamples(ctx, "m1")
	if err != nil || n != 1 {
		t.Fatalf("delete=%d err=%v", n, err)
	}
	samples, _ := svc.ListSamples(ctx, "", 0)
	if len(samples) != 0 {
		t.Fatalf("samples after delete=%d", len(samples))
	}
}

// 保险：运行请求可 JSON 往返（管理 API 直传）。
func TestCodexModelTraceRunRequestJSON(t *testing.T) {
	raw := `{"kind":"detect","models":["gpt-6-astra"],"account_ids":[244]}`
	var req CodexModelTraceRunRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if req.Kind != "detect" || req.Models[0] != "gpt-6-astra" || req.AccountIDs[0] != 244 {
		t.Fatalf("decode: %+v", req)
	}
}
