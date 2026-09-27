package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// codex_modeltrace：Codex 模型降智（换模）检测 —— 自建数字分布指纹库。
//
// 背景：上游可能把请求的模型换成另一档模型服务（降智）。各模型对「生成
// 随机整数」有稳定的个体数字分布偏差（跨模型可区分，移植自 codex-inspector
// 计划 Task 11 的 ModelTrace 算法规格；上游 unified_bank 不公开，改为自建库：
// 用自有官号对每个候选模型采集样本，跨模型区分性使自建库有效）。
//
// 流程：
//   - 建库（bank）：对每个候选模型发 N 次「生成 292..332 个 1..355 整数」
//     挑战，ParseNumbers 提取数字游程存样本表，聚合出每模型 355 维边际质心
//     + 74 维顺序块质心/方差，LOO 交叉校准 softmax β，整库 JSON 原子落库。
//   - 检测（detect）：对（账号, 模型）对发多次挑战，回答与库比对：
//     hellinger + 顺序块标准化欧氏 z 融合 + softmax，verdict = 最贴近的
//     库内模型；verdict ≠ 请求模型 = 疑似换模。
//
// 探测复用打票的 codex 线格式基建（openai_ticket_probe_wire.go）：会话身份、
// 手工 H1 写出器、utls 指纹拨号。出口用账号自身静态代理（检测的是「我们的
// 账号在真实出口下被服务的模型」）。任务由管理 API 手动触发，异步执行，
// 状态经 Status 轮询；无后台循环。

// SettingKeyCodexModelTrace 降智检测配置（JSON）的设置键。
const SettingKeyCodexModelTrace = "codex_modeltrace"

// codexModelTraceSettingsCacheTTL 配置缓存时长（DB 直改 30s 内生效）。
const codexModelTraceSettingsCacheTTL = 30 * time.Second

// CodexModelTraceDefaultModels 默认候选模型（8 档可选）。
var CodexModelTraceDefaultModels = []string{
	"gpt-5.4", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
}

// codexModelTraceBodyLimit SSE 响应体读取上限（300 个数字的回答远小于此）。
const codexModelTraceBodyLimit = 4 << 20

// CodexModelTraceSettings 降智检测配置。
type CodexModelTraceSettings struct {
	Models           []string `json:"models"`         // 建库/检测的候选模型
	AccountIDs       []int64  `json:"account_ids"`    // 探测用官号（轮换）
	BankRepeats      int      `json:"bank_repeats"`   // 建库每模型采样次数
	DetectRepeats    int      `json:"detect_repeats"` // 检测每（账号,模型）回答数
	Concurrency      int      `json:"concurrency"`    // 并发探测数
	ProbeTimeoutSecs int      `json:"probe_timeout_secs"`
	RequestGapMS     int      `json:"request_gap_ms"` // 相邻探测间隔（限速）
}

// DefaultCodexModelTraceSettings 默认配置。
func DefaultCodexModelTraceSettings() CodexModelTraceSettings {
	return CodexModelTraceSettings{
		Models:           append([]string{}, CodexModelTraceDefaultModels...),
		AccountIDs:       []int64{},
		BankRepeats:      12,
		DetectRepeats:    5,
		Concurrency:      2,
		ProbeTimeoutSecs: 180,
		RequestGapMS:     800,
	}
}

// Validate 校验并归一化配置。
func (c *CodexModelTraceSettings) Validate() error {
	if len(c.Models) == 0 {
		return errors.New("models 不能为空")
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		m = strings.TrimSpace(m)
		if m == "" {
			return errors.New("models 含空模型名")
		}
		if seen[m] {
			return fmt.Errorf("models 重复：%s", m)
		}
		seen[m] = true
	}
	if len(c.Models) > 20 {
		return errors.New("models 最多 20 个")
	}
	for _, id := range c.AccountIDs {
		if id <= 0 {
			return errors.New("account_ids 必须是正整数")
		}
	}
	if c.BankRepeats < 1 || c.BankRepeats > 41 {
		return errors.New("bank_repeats 须在 1..41（挑战长度池上限）")
	}
	if c.DetectRepeats < 1 || c.DetectRepeats > 41 {
		return errors.New("detect_repeats 须在 1..41")
	}
	if c.Concurrency < 1 || c.Concurrency > 8 {
		return errors.New("concurrency 须在 1..8")
	}
	if c.ProbeTimeoutSecs < 30 || c.ProbeTimeoutSecs > 600 {
		return errors.New("probe_timeout_secs 须在 30..600")
	}
	if c.RequestGapMS < 0 || c.RequestGapMS > 60000 {
		return errors.New("request_gap_ms 须在 0..60000")
	}
	return nil
}

// CodexModelTraceSample 建库原始样本。
type CodexModelTraceSample struct {
	ID            int64     `json:"id"`
	Model         string    `json:"model"`
	AccountID     int64     `json:"account_id"`
	ExpectedCount int       `json:"expected_count"`
	Numbers       []int     `json:"numbers"`
	NumbersCount  int       `json:"numbers_count"`
	LatencyMS     int       `json:"latency_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

// CodexModelTraceResult 任务内一对（账号,模型）的结论。
type CodexModelTraceResult struct {
	ID           int64     `json:"id"`
	TaskID       string    `json:"task_id"`
	Kind         string    `json:"kind"` // bank | detect
	AccountID    int64     `json:"account_id"`
	Model        string    `json:"model"`   // 请求模型
	Verdict      string    `json:"verdict"` // 判定实际模型（bank=ok/insufficient）
	Probability  float64   `json:"probability"`
	Match        bool      `json:"match"` // verdict == model
	TopHits      int       `json:"top_hits"`
	ValidRuns    int       `json:"valid_runs"`
	Failures     int       `json:"failures"`
	AvgLatencyMS int       `json:"avg_latency_ms"`
	Reasons      string    `json:"reasons"`
	CreatedAt    time.Time `json:"created_at"`
}

// CodexModelTraceRepository 降智检测数据访问接口。
type CodexModelTraceRepository interface {
	InsertSample(ctx context.Context, s *CodexModelTraceSample) error
	ListSamples(ctx context.Context, model string, limit int) ([]*CodexModelTraceSample, error)
	DeleteSamples(ctx context.Context, model string) (int64, error)
	SaveBank(ctx context.Context, bankJSON string) error
	GetBank(ctx context.Context) (string, time.Time, bool, error)
	InsertResult(ctx context.Context, r *CodexModelTraceResult) error
	ListResults(ctx context.Context, taskID string, limit, offset int) ([]*CodexModelTraceResult, error)
}

// CodexModelTraceTaskStatus 任务运行状态。
type CodexModelTraceTaskStatus struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // bank | detect
	StartedAt time.Time `json:"started_at"`
	Total     int       `json:"total"`
	Done      int       `json:"done"`
	Finished  bool      `json:"finished"`
	LastError string    `json:"last_error,omitempty"`
}

// CodexModelTraceRunRequest 手动触发请求。
type CodexModelTraceRunRequest struct {
	Kind       string   `json:"kind"` // bank | detect
	Models     []string `json:"models,omitempty"`
	AccountIDs []int64  `json:"account_ids,omitempty"`
}

// CodexModelTraceService 降智检测服务。
type CodexModelTraceService struct {
	repo            CodexModelTraceRepository
	accountRepo     AccountRepository
	tokenProvider   *OpenAITokenProvider
	settingRepo     SettingRepository
	profileResolver func(*Account) *tlsfingerprint.Profile

	settingsMu     sync.RWMutex
	settingsCache  CodexModelTraceSettings
	settingsLoaded time.Time

	taskMu   sync.Mutex
	task     *CodexModelTraceTaskStatus
	taskDone func() // 任务结束时清理钩子（测试用）

	// probeFn 可注入的探测实现（测试用）；nil 时走真实上游。
	probeFn func(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge) codexModelTraceProbeOutcome
}

// NewCodexModelTraceService 构造降智检测服务。
func NewCodexModelTraceService(
	repo CodexModelTraceRepository,
	accountRepo AccountRepository,
	tokenProvider *OpenAITokenProvider,
	settingRepo SettingRepository,
	profileResolver func(*Account) *tlsfingerprint.Profile,
) *CodexModelTraceService {
	return &CodexModelTraceService{
		repo:            repo,
		accountRepo:     accountRepo,
		tokenProvider:   tokenProvider,
		settingRepo:     settingRepo,
		profileResolver: profileResolver,
	}
}

// GetSettings 返回当前配置（带 30s 缓存）。
func (s *CodexModelTraceService) GetSettings(ctx context.Context) CodexModelTraceSettings {
	s.settingsMu.RLock()
	if !s.settingsLoaded.IsZero() && time.Since(s.settingsLoaded) < codexModelTraceSettingsCacheTTL {
		cached := s.settingsCache
		s.settingsMu.RUnlock()
		return cached
	}
	s.settingsMu.RUnlock()

	settings := DefaultCodexModelTraceSettings()
	if s.settingRepo != nil {
		if raw, err := s.settingRepo.GetValue(ctx, SettingKeyCodexModelTrace); err == nil && strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &settings); err != nil {
				slog.Warn("codex_modeltrace settings decode failed, using defaults", "error", err)
				settings = DefaultCodexModelTraceSettings()
			}
		}
	}
	_ = settings.Validate()

	s.settingsMu.Lock()
	s.settingsCache, s.settingsLoaded = settings, time.Now()
	s.settingsMu.Unlock()
	return settings
}

// UpdateSettings 校验并保存配置。
func (s *CodexModelTraceService) UpdateSettings(ctx context.Context, settings CodexModelTraceSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if s.settingRepo == nil {
		return errors.New("setting repository unavailable")
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCodexModelTrace, string(raw)); err != nil {
		return fmt.Errorf("save codex modeltrace settings: %w", err)
	}
	s.settingsMu.Lock()
	s.settingsCache, s.settingsLoaded = settings, time.Now()
	s.settingsMu.Unlock()
	return nil
}

// beginTask 占用任务槽（同一时刻只允许一个任务在跑）。
func (s *CodexModelTraceService) beginTask(kind string, total int) (string, error) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.task != nil && !s.task.Finished {
		return "", fmt.Errorf("任务 %s（%s）仍在运行，请稍后再试", s.task.ID, s.task.Kind)
	}
	task := &CodexModelTraceTaskStatus{
		ID:        "mt" + time.Now().Format("01021504") + "-" + uuid.NewString()[:8],
		Kind:      kind,
		StartedAt: time.Now(),
		Total:     total,
	}
	s.task = task
	return task.ID, nil
}

// snapshotTask 任务状态快照。
func (s *CodexModelTraceService) snapshotTask() *CodexModelTraceTaskStatus {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.task == nil {
		return nil
	}
	snap := *s.task
	return &snap
}

// stepTask 推进进度。
func (s *CodexModelTraceService) stepTask() {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.task != nil {
		s.task.Done++
	}
}

// finishTask 标记任务结束并记录错误。
func (s *CodexModelTraceService) finishTask(errMsg string) {
	s.taskMu.Lock()
	if s.task != nil {
		s.task.Finished = true
		if errMsg != "" {
			s.task.LastError = errMsg
		}
	}
	done := s.taskDone
	s.taskMu.Unlock()
	if done != nil {
		done()
	}
}

// resolveAccounts 解析探测账号：只留 openai OAuth 濗活账号。
func (s *CodexModelTraceService) resolveAccounts(ctx context.Context, accountIDs []int64) ([]*Account, error) {
	if s.accountRepo == nil {
		return nil, errors.New("account repository unavailable")
	}
	if len(accountIDs) == 0 {
		return nil, errors.New("account_ids 为空，请先在配置里指定探测官号")
	}
	var out []*Account
	for _, id := range accountIDs {
		account, err := s.accountRepo.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("加载账号 %d: %w", id, err)
		}
		if account == nil {
			return nil, fmt.Errorf("账号 %d 不存在", id)
		}
		if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
			return nil, fmt.Errorf("账号 %d 不是 openai OAuth 官号", id)
		}
		if !account.IsActive() {
			return nil, fmt.Errorf("账号 %d 未激活", id)
		}
		out = append(out, account)
	}
	return out, nil
}

// RunBankBuild 异步启动建库任务。
func (s *CodexModelTraceService) RunBankBuild(ctx context.Context, req CodexModelTraceRunRequest) (*CodexModelTraceTaskStatus, error) {
	settings := s.GetSettings(ctx)
	models := settings.Models
	if len(req.Models) > 0 {
		models = req.Models
	}
	accounts, err := s.resolveAccounts(ctx, firstNonEmptyIDs(req.AccountIDs, settings.AccountIDs))
	if err != nil {
		return nil, err
	}
	taskID, err := s.beginTask("bank", len(models)*settings.BankRepeats)
	if err != nil {
		return nil, err
	}
	go s.executeBankBuild(context.Background(), taskID, models, accounts, settings)
	return s.snapshotTask(), nil
}

// RunDetect 异步启动检测任务。
func (s *CodexModelTraceService) RunDetect(ctx context.Context, req CodexModelTraceRunRequest) (*CodexModelTraceTaskStatus, error) {
	settings := s.GetSettings(ctx)
	models := settings.Models
	if len(req.Models) > 0 {
		models = req.Models
	}
	accounts, err := s.resolveAccounts(ctx, firstNonEmptyIDs(req.AccountIDs, settings.AccountIDs))
	if err != nil {
		return nil, err
	}
	taskID, err := s.beginTask("detect", len(models)*len(accounts))
	if err != nil {
		return nil, err
	}
	go s.executeDetect(context.Background(), taskID, models, accounts, settings)
	return s.snapshotTask(), nil
}

func firstNonEmptyIDs(a, b []int64) []int64 {
	if len(a) > 0 {
		return a
	}
	return b
}

// codexModelTraceProbeOutcome 单次探测的可判定产物。
type codexModelTraceProbeOutcome struct {
	numbers      []int
	expected     int
	textLength   int
	latencyMS    int
	status       int
	reason       string // 失败原因短语（空=成功）
	accountAbort bool   // 401/403/429：该账号跳过剩余探测
}

// executeBankBuild 建库：逐模型采样 → 全量聚合 BuildBank → 原子落库。
func (s *CodexModelTraceService) executeBankBuild(ctx context.Context, taskID string, models []string, accounts []*Account, settings CodexModelTraceSettings) {
	failMsg := func(err error) string {
		s.finishTask(err.Error())
		slog.Warn("codex_modeltrace bank build failed", "task", taskID, "error", err)
		return err.Error()
	}

	challenges := modeltrace.GenerateChallenges(settings.BankRepeats, modeltrace.CryptoRandIntn)
	type unitWork struct {
		model     string
		account   *Account
		challenge modeltrace.Challenge
	}
	units := make([]unitWork, 0, len(models)*settings.BankRepeats)
	for mi, model := range models {
		for i := 0; i < settings.BankRepeats; i++ {
			units = append(units, unitWork{
				model:     model,
				account:   accounts[(mi*settings.BankRepeats+i)%len(accounts)],
				challenge: challenges[i],
			})
		}
	}

	abortedAccounts := map[int64]bool{} // 账号级熔断（401/403/429）后跳过其剩余采样
	type modelStat struct {
		stored, failures, latSum int
		reasons                  []string
	}
	stats := map[string]*modelStat{}
	var mu sync.Mutex
	work := make(chan unitWork)
	var wg sync.WaitGroup
	for w := 0; w < settings.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range work {
				mu.Lock()
				skip := abortedAccounts[u.account.ID]
				mu.Unlock()
				out := codexModelTraceProbeOutcome{reason: "skipped"}
				if !skip {
					out = s.probe(ctx, u.account, u.model, u.challenge, settings)
				}
				mu.Lock()
				st := stats[u.model]
				if st == nil {
					st = &modelStat{}
					stats[u.model] = st
				}
				threshold := modeltrace.ValidAnswerThreshold(u.challenge.ExpectedCount)
				switch {
				case out.reason == "":
					if len(out.numbers) >= threshold {
						st.stored++
						st.latSum += out.latencyMS
						if err := s.repo.InsertSample(ctx, &CodexModelTraceSample{
							Model:         u.model,
							AccountID:     u.account.ID,
							ExpectedCount: u.challenge.ExpectedCount,
							Numbers:       out.numbers,
							LatencyMS:     out.latencyMS,
						}); err != nil {
							slog.Warn("codex_modeltrace insert sample failed", "model", u.model, "error", err)
						}
					} else {
						st.failures++
						st.reasons = appendUnique(st.reasons, fmt.Sprintf("%s:数字不足(%d<%d)", u.model, len(out.numbers), threshold))
					}
				default:
					st.failures++
					st.reasons = appendUnique(st.reasons, fmt.Sprintf("%s:%s", u.model, out.reason))
					if out.accountAbort {
						abortedAccounts[u.account.ID] = true
					}
				}
				mu.Unlock()
				s.stepTask()
				if settings.RequestGapMS > 0 {
					select {
					case <-time.After(time.Duration(settings.RequestGapMS) * time.Millisecond):
					case <-ctx.Done():
					}
				}
			}
		}()
	}
	for _, u := range units {
		work <- u
	}
	close(work)
	wg.Wait()

	// 聚合全量样本重建指纹库（历史样本一并参与）。
	samples, err := s.repo.ListSamples(ctx, "", 0)
	if err != nil {
		failMsg(fmt.Errorf("读取样本: %w", err))
		return
	}
	bankSamples := make([]modeltrace.Sample, 0, len(samples))
	for _, sm := range samples {
		bankSamples = append(bankSamples, modeltrace.Sample{
			Model: sm.Model, AccountID: sm.AccountID,
			ExpectedCount: sm.ExpectedCount, Numbers: sm.Numbers, LatencyMS: sm.LatencyMS,
		})
	}
	bank, err := modeltrace.BuildBank(bankSamples)
	if err != nil {
		failMsg(err)
		return
	}
	raw, err := modeltrace.MarshalBank(bank)
	if err != nil {
		failMsg(err)
		return
	}
	if err := s.repo.SaveBank(ctx, string(raw)); err != nil {
		failMsg(err)
		return
	}

	for _, model := range models {
		st := stats[model]
		if st == nil {
			st = &modelStat{}
		}
		verdict := "ok"
		if st.stored < modeltrace.MinSamplesPerModel {
			verdict = "insufficient"
		}
		avg := 0
		if st.stored > 0 {
			avg = st.latSum / st.stored
		}
		_ = s.repo.InsertResult(ctx, &CodexModelTraceResult{
			TaskID: taskID, Kind: "bank", AccountID: 0, Model: model,
			Verdict: verdict, ValidRuns: st.stored, Failures: st.failures,
			AvgLatencyMS: avg, Reasons: strings.Join(st.reasons, "; "),
		})
	}
	s.finishTask("")
	slog.Info("codex_modeltrace bank built", "task", taskID, "models", len(models), "bank_models", len(bank.Profiles))
}

// executeDetect 检测：每（账号,模型）对多次探测 → 与库比对 → 逐对结论落库。
func (s *CodexModelTraceService) executeDetect(ctx context.Context, taskID string, models []string, accounts []*Account, settings CodexModelTraceSettings) {
	bankJSON, _, ok, err := s.repo.GetBank(ctx)
	if err != nil {
		s.finishTask(fmt.Sprintf("读取指纹库失败: %v", err))
		return
	}
	if !ok {
		s.finishTask("指纹库未构建，请先运行建库任务")
		return
	}
	bank, err := modeltrace.ParseBank([]byte(bankJSON))
	if err != nil {
		s.finishTask(fmt.Sprintf("指纹库解析失败: %v", err))
		return
	}

	type pairWork struct {
		account *Account
		model   string
	}
	pairs := make([]pairWork, 0, len(accounts)*len(models))
	for _, account := range accounts {
		for _, model := range models {
			pairs = append(pairs, pairWork{account: account, model: model})
		}
	}

	var wg sync.WaitGroup
	work := make(chan pairWork)
	for w := 0; w < settings.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				s.detectPair(ctx, taskID, bank, p.account, p.model, settings)
				s.stepTask()
				if settings.RequestGapMS > 0 {
					select {
					case <-time.After(time.Duration(settings.RequestGapMS) * time.Millisecond):
					case <-ctx.Done():
					}
				}
			}
		}()
	}
	for _, p := range pairs {
		work <- p
	}
	close(work)
	wg.Wait()
	s.finishTask("")
}

// detectPair 单（账号,模型）对的检测。
func (s *CodexModelTraceService) detectPair(ctx context.Context, taskID string, bank *modeltrace.Bank, account *Account, model string, settings CodexModelTraceSettings) {
	challenges := modeltrace.GenerateChallenges(settings.DetectRepeats, modeltrace.CryptoRandIntn)
	answers := make([]modeltrace.Answer, 0, settings.DetectRepeats)
	failures := 0
	latSum, latN := 0, 0
	var reasons []string
	for i := 0; i < settings.DetectRepeats; i++ {
		out := s.probe(ctx, account, model, challenges[i], settings)
		switch {
		case out.reason == "":
			answers = append(answers, modeltrace.Answer{
				ExpectedCount: challenges[i].ExpectedCount,
				Numbers:       out.numbers,
			})
			latSum += out.latencyMS
			latN++
		default:
			failures++
			reasons = appendUnique(reasons, out.reason)
			if out.accountAbort {
				reasons = appendUnique(reasons, fmt.Sprintf("账号 %d 熔断，跳过剩余 %d 次", account.ID, settings.DetectRepeats-i-1))
				i = settings.DetectRepeats // break 外层
			}
		}
		if settings.RequestGapMS > 0 {
			select {
			case <-time.After(time.Duration(settings.RequestGapMS) * time.Millisecond):
			case <-ctx.Done():
			}
		}
	}
	verdict, err := bank.Analyze(answers)
	if err != nil {
		slog.Warn("codex_modeltrace analyze failed", "account", account.ID, "model", model, "error", err)
		verdict = &modeltrace.Verdict{Reasons: []string{err.Error()}}
	}
	if len(verdict.Reasons) > 0 {
		reasons = append(reasons, verdict.Reasons...)
	}
	avg := 0
	if latN > 0 {
		avg = latSum / latN
	}
	result := &CodexModelTraceResult{
		TaskID: taskID, Kind: "detect", AccountID: account.ID, Model: model,
		Verdict: verdict.Model, Probability: verdict.Probability,
		Match:   verdict.Model != "" && verdict.Model == model,
		TopHits: verdict.TopHits, ValidRuns: verdict.ValidRuns,
		Failures: failures, AvgLatencyMS: avg,
		Reasons: strings.Join(reasons, "; "),
	}
	if err := s.repo.InsertResult(ctx, result); err != nil {
		slog.Warn("codex_modeltrace insert result failed", "account", account.ID, "model", model, "error", err)
	}
	slog.Info("codex_modeltrace_detect",
		"task", taskID, "account_id", account.ID, "model", model,
		"verdict", verdict.Model, "match", result.Match,
		"probability", verdict.Probability, "valid_runs", verdict.ValidRuns, "failures", failures)
}

// probe 单次挑战探测（真实上游或测试注入）。
func (s *CodexModelTraceService) probe(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge, settings CodexModelTraceSettings) codexModelTraceProbeOutcome {
	if s.probeFn != nil {
		return s.probeFn(ctx, account, model, challenge)
	}
	return s.probeUpstream(ctx, account, model, challenge, settings)
}

// probeUpstream 真实探测：账号静态出口 + codex 线格式 + SSE 解析。
func (s *CodexModelTraceService) probeUpstream(ctx context.Context, account *Account, model string, challenge modeltrace.Challenge, settings CodexModelTraceSettings) codexModelTraceProbeOutcome {
	out := codexModelTraceProbeOutcome{expected: challenge.ExpectedCount}
	if s.tokenProvider == nil {
		out.reason = "no_token_provider"
		return out
	}
	token, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		out.reason = "token_error"
		return out
	}

	client, closeClient, err := s.probeClient(account)
	if err != nil {
		out.reason = "client_error"
		return out
	}
	defer closeClient()

	identity := resolveCodexModelTraceIdentity(account)
	body := buildCodexModelTraceRequestBody(model, challenge.Prompt, identity)
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.ProbeTimeoutSecs)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost,
		"https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(body))
	if err != nil {
		out.reason = "request_error"
		return out
	}
	req.Host = "chatgpt.com"
	req.Header.Set("authorization", "Bearer "+token)
	if acctID := account.GetChatGPTAccountID(); acctID != "" {
		req.Header.Set("chatgpt-account-id", acctID)
	}
	req.Header.Set("user-agent", CodexCanonicalUserAgent())
	req.Header.Set("originator", openai.CodexDefaultOriginator)
	req.Header.Set("version", CodexCanonicalClientVersion())
	req.Header.Set("openai-beta", "responses=experimental")
	req.Header.Set("session_id", identity.sessionID)
	req.Header.Set("conversation_id", identity.threadID)
	req.Header.Set("x-codex-installation-id", identity.installationID)
	req.Header.Set("x-codex-window-id", identity.windowID)
	req.Header.Set("x-codex-turn-metadata", buildOpenAITicketTurnMetadataJSON(identity))
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("content-type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		out.reason = "network_error"
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	out.latencyMS = int(time.Since(start).Milliseconds())
	out.status = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		out.reason = "http_" + strconv.Itoa(resp.StatusCode)
		out.accountAbort = resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		return out
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, codexModelTraceBodyLimit))
	stream := parseModelTraceSSE(data)
	switch {
	case stream.Failed != "":
		out.reason = "stream_failed:" + stream.Failed
	case !stream.Completed:
		out.reason = "stream_incomplete"
	default:
		out.numbers = modeltrace.ParseNumbers(stream.Text)
		out.textLength = len(stream.Text)
	}
	return out
}

// probeClient 按账号出口形态构建探测 client：账号静态代理优先（检测的是
// 真实出口下被服务的模型）；https 代理回落普通 transport（与打票一致）。
func (s *CodexModelTraceService) probeClient(account *Account) (*http.Client, func(), error) {
	proxyURL := ""
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	profile := s.resolveProfile(account)
	if pu, err := url.Parse(proxyURL); err == nil && pu != nil && strings.EqualFold(pu.Scheme, "https") && pu.Host != "" {
		transport := &http.Transport{
			Proxy:               http.ProxyURL(pu),
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		}
		return &http.Client{Transport: transport}, func() { transport.CloseIdleConnections() }, nil
	}
	var dial func(ctx context.Context, network, addr string) (net.Conn, error)
	if proxyURL == "" {
		dial = tlsfingerprint.NewDialer(profile, nil).DialTLSContext
	} else {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return nil, nil, fmt.Errorf("parse proxy url: %w", err)
		}
		switch strings.ToLower(pu.Scheme) {
		case "socks5", "socks5h":
			dial = tlsfingerprint.NewSOCKS5ProxyDialer(profile, pu).DialTLSContext
		default:
			dial = tlsfingerprint.WarmHTTPProxyDialerFor(profile, pu, nil)
		}
	}
	wire := &openAITicketWireRoundTripper{dial: dial}
	return &http.Client{Transport: wire}, wire.Close, nil
}

// resolveProfile 探测连接的 TLS 指纹模板（默认内置 CodexProfile）。
func (s *CodexModelTraceService) resolveProfile(account *Account) *tlsfingerprint.Profile {
	if s.profileResolver != nil {
		if profile := s.profileResolver(account); profile != nil {
			return profile
		}
	}
	return tlsfingerprint.CodexProfile
}

// resolveCodexModelTraceIdentity 检测身份：复用打票的收敛/派生逻辑，但
// thread 用检测专属命名空间（与打票探测、真实转发的线程空间互不干扰）。
func resolveCodexModelTraceIdentity(account *Account) *openAITicketProbeIdentity {
	seed, _ := codexFingerprintSeed(account.Extra)
	accountKey := strconv.FormatInt(account.ID, 10)
	installationID := resolveConvergedInstallationID(account, seed)
	if installationID == "" {
		installationID = deriveStableUUIDv4("sub2api:codex-modeltrace-install:v1:" + accountKey)
	}
	sessionID := resolveConvergedSessionID(seed)
	if sessionID == "" {
		sessionID = deriveStableUUIDv4("sub2api:codex-modeltrace-session:v1:" + accountKey)
	}
	threadID := deriveStableUUIDv4("sub2api:codex-modeltrace-thread:v1:" + accountKey)
	return &openAITicketProbeIdentity{
		installationID: installationID,
		sessionID:      sessionID,
		threadID:       threadID,
		turnID:         uuid.NewString(),
		windowID:       threadID + ":0",
		turnStartedAt:  time.Now().UnixMilli(),
	}
}

// buildCodexModelTraceRequestBody 检测探测体：与打票探测体同构（codex-rs
// ResponsesApiRequest 字段序），仅 input 文本换成挑战提示语。
func buildCodexModelTraceRequestBody(model, prompt string, ids *openAITicketProbeIdentity) []byte {
	var b bytes.Buffer
	b.WriteString(`{"model":`)
	writeJSONString(&b, model)
	b.WriteString(`,"instructions":`)
	writeJSONString(&b, openAITicketProbeInstructions)
	b.WriteString(`,"input":[{"type":"message","role":"user","content":`)
	writeJSONString(&b, prompt)
	b.WriteString(`}],"tool_choice":"auto","parallel_tool_calls":false,` +
		`"reasoning":{"effort":"low","summary":"auto"},"store":false,"stream":true,` +
		`"include":["reasoning.encrypted_content"],"prompt_cache_key":`)
	writeJSONString(&b, ids.sessionID)
	b.WriteString(`,"client_metadata":{"x-codex-installation-id":`)
	writeJSONString(&b, ids.installationID)
	b.WriteString(`,"session_id":`)
	writeJSONString(&b, ids.sessionID)
	b.WriteString(`,"thread_id":`)
	writeJSONString(&b, ids.threadID)
	b.WriteString(`,"x-codex-window-id":`)
	writeJSONString(&b, ids.windowID)
	b.WriteString(`,"x-codex-turn-metadata":`)
	writeJSONString(&b, buildOpenAITicketTurnMetadataJSON(ids))
	b.WriteString(`}}`)
	return b.Bytes()
}

// modelTraceStreamResult SSE 流解析产物。
type modelTraceStreamResult struct {
	Text      string
	Completed bool
	Failed    string
}

// parseModelTraceSSE 从 SSE 字节流提取最终文本与事件状态：
// response.output_text.delta 累加文本；response.completed 兜底从
// response.output[].content[].output_text 提取全文；response.failed / error
// 记为失败；无 completed 事件由调用方按 stream_incomplete 处理。
func parseModelTraceSSE(data []byte) modelTraceStreamResult {
	var result modelTraceStreamResult
	var text strings.Builder
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		evt := gjson.Parse(payload)
		switch evt.Get("type").String() {
		case "response.output_text.delta":
			text.WriteString(evt.Get("delta").String())
		case "response.completed":
			result.Completed = true
			if text.Len() == 0 {
				// 兜底：从终态 response.output 提取全部 output_text。
				for _, item := range evt.Get("response.output").Array() {
					for _, c := range item.Get("content").Array() {
						if c.Get("type").String() == "output_text" {
							text.WriteString(c.Get("text").String())
						}
					}
				}
			}
		case "response.failed":
			msg := evt.Get("response.error.message").String()
			if msg == "" {
				msg = evt.Get("response.error").String()
			}
			result.Failed = msg
		case "error":
			msg := evt.Get("error.message").String()
			if msg == "" {
				msg = evt.Get("error").String()
			}
			result.Failed = msg
		}
	}
	result.Text = text.String()
	return result
}

// Status 聚合状态：任务 + 指纹库 + 每模型样本数。
func (s *CodexModelTraceService) Status(ctx context.Context) (map[string]any, error) {
	// 注意：typed-nil 指针装进 any 会非 nil，这里显式落 nil 字面量。
	status := map[string]any{}
	if task := s.snapshotTask(); task != nil {
		status["task"] = task
	} else {
		status["task"] = nil
	}
	if s.repo == nil {
		return status, nil
	}
	bankJSON, updatedAt, ok, err := s.repo.GetBank(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		bank, err := modeltrace.ParseBank([]byte(bankJSON))
		if err == nil {
			models := make([]map[string]any, 0, len(bank.Profiles))
			for _, p := range bank.Profiles {
				models = append(models, map[string]any{
					"model":   p.Model,
					"samples": p.Samples,
				})
			}
			status["bank"] = map[string]any{
				"built_at":       updatedAt,
				"models":         models,
				"calibration":    bank.Calibration,
				"ordered_weight": bank.OrderedWeight,
			}
		}
	}
	samples, err := s.repo.ListSamples(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, sm := range samples {
		counts[sm.Model]++
	}
	status["sample_counts"] = counts
	return status, nil
}

// ListResults 查询结果。
func (s *CodexModelTraceService) ListResults(ctx context.Context, taskID string, limit, offset int) ([]*CodexModelTraceResult, error) {
	if s.repo == nil {
		return nil, nil
	}
	return s.repo.ListResults(ctx, taskID, limit, offset)
}

// ListSamples 查询样本（排查用）。
func (s *CodexModelTraceService) ListSamples(ctx context.Context, model string, limit int) ([]*CodexModelTraceSample, error) {
	if s.repo == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return s.repo.ListSamples(ctx, model, limit)
}

// DeleteSamples 清理样本（model 空 = 全部）。
func (s *CodexModelTraceService) DeleteSamples(ctx context.Context, model string) (int64, error) {
	if s.repo == nil {
		return 0, nil
	}
	return s.repo.DeleteSamples(ctx, model)
}

// appendUnique 追加不重复的短语。
func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
