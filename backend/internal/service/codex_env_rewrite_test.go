package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// codexEnvTestRepo 内存版 SettingRepository（构造函数用最小实现满足接口）。
type codexEnvTestRepo struct {
	data map[string]string
}

func newCodexEnvTestRepo() *codexEnvTestRepo { return &codexEnvTestRepo{data: map[string]string{}} }

func (r *codexEnvTestRepo) Get(ctx context.Context, key string) (*Setting, error) { return nil, nil }
func (r *codexEnvTestRepo) GetValue(ctx context.Context, key string) (string, error) {
	return r.data[key], nil
}
func (r *codexEnvTestRepo) Set(ctx context.Context, key, value string) error {
	r.data[key] = value
	return nil
}
func (r *codexEnvTestRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = r.data[k]
	}
	return out, nil
}
func (r *codexEnvTestRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	for k, v := range settings {
		r.data[k] = v
	}
	return nil
}
func (r *codexEnvTestRepo) GetAll(ctx context.Context) (map[string]string, error) { return r.data, nil }
func (r *codexEnvTestRepo) Delete(ctx context.Context, key string) error {
	delete(r.data, key)
	return nil
}

// codexEnvBody 拼一个带环境块的 /responses body（字段序刻意非字母序，
// 用于验证保序重建）。
func codexEnvBody(envText string, escaped bool) []byte {
	body := map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "message", "role": "system", "content": "sys"},
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": envText},
					map[string]any{"type": "input_text", "text": "普通用户消息"},
				},
			},
		},
		"instructions": "You are Codex",
		"store":        false,
	}
	raw, _ := json.Marshal(body) // map marshal 字母序；只用于基线（键序测试单独构造）
	if escaped {
		return raw // json.Marshal 默认把 < 转成 <，正好模拟宿主桥接重编形态
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

const codexEnvSampleBlock = `<environment_context>
  <timezone>America/New_York</timezone>
  <current_date>2026-09-27</current_date>
  <os>Windows 11</os>
</environment_context>`

func TestRewriteCodexEnvironmentBodyTokyoRollover(t *testing.T) {
	// UTC 20:30 时东京已是次日 05:30 —— 日期必须按目标时区取值。
	now := time.Date(2026, 9, 27, 20, 30, 0, 0, time.UTC)
	out, blocks, changed, err := rewriteCodexEnvironmentBody(codexEnvBody(codexEnvSampleBlock, false), "Asia/Tokyo", now)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !changed || blocks != 1 {
		t.Fatalf("changed=%v blocks=%d", changed, blocks)
	}
	text := gjsonGetText(t, out)
	if !strings.Contains(text, "<timezone>Asia/Tokyo</timezone>") {
		t.Fatalf("timezone not rewritten: %q", text)
	}
	if !strings.Contains(text, "<current_date>2026-09-28</current_date>") {
		t.Fatalf("date should roll over to Tokyo date 2026-09-28: %q", text)
	}
	if strings.Contains(text, "America/New_York") {
		t.Fatalf("old tz leaked: %q", text)
	}
	// 非环境块的消息不动。
	if !strings.Contains(string(out), "普通用户消息") {
		t.Fatalf("unrelated content lost: %s", out)
	}
}

func TestRewriteCodexEnvironmentBodyLAAcrossMidnight(t *testing.T) {
	now := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC) // UTC 凌晨 3 点 = LA 前一天 20:00
	out, _, changed, err := rewriteCodexEnvironmentBody(codexEnvBody(codexEnvSampleBlock, false), "America/Los_Angeles", now)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	text := gjsonGetText(t, out)
	if !strings.Contains(text, "<current_date>2026-09-26</current_date>") {
		t.Fatalf("expected LA date 2026-09-26: %q", text)
	}
}

func gjsonGetText(t *testing.T, body []byte) string {
	t.Helper()
	parsed := gjson.ParseBytes(body)
	arr := parsed.Get("input").Array()
	for _, item := range arr {
		if item.Get("role").String() != "user" {
			continue
		}
		for _, c := range item.Get("content").Array() {
			if strings.HasPrefix(c.Get("text").String(), "<environment_context>") {
				return c.Get("text").String()
			}
		}
	}
	t.Fatalf("environment_context block not found in %s", body)
	return ""
}

func TestRewriteCodexEnvironmentBodyEscapedForm(t *testing.T) {
	// 宿主桥接重编过的转义形态（<）也能命中并改写。
	escaped := codexEnvBody(codexEnvSampleBlock, true)
	if !bytes.Contains(escaped, []byte("\\u003cenvironment_context")) {
		t.Fatalf("fixture not escaped: %s", escaped)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	out, blocks, changed, err := rewriteCodexEnvironmentBody(escaped, "Asia/Seoul", now)
	if err != nil || !changed || blocks != 1 {
		t.Fatalf("changed=%v blocks=%d err=%v", changed, blocks, err)
	}
	text := gjsonGetText(t, out)
	if !strings.Contains(text, "<timezone>Asia/Seoul</timezone>") {
		t.Fatalf("escaped body not rewritten: %q", text)
	}
}

func TestRewriteCodexEnvironmentBodySelfClosingDate(t *testing.T) {
	block := "<environment_context>\n  <timezone>Europe/London</timezone>\n  <current_date />\n</environment_context>"
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	out, blocks, changed, err := rewriteCodexEnvironmentBody(codexEnvBody(block, false), "Asia/Singapore", now)
	if err != nil || !changed || blocks != 1 {
		t.Fatalf("changed=%v blocks=%d err=%v", changed, blocks, err)
	}
	text := gjsonGetText(t, out)
	if !strings.Contains(text, "<timezone>Asia/Singapore</timezone>") {
		t.Fatalf("timezone not rewritten: %q", text)
	}
	if !strings.Contains(text, "<current_date />") {
		t.Fatalf("self-closing date must stay untouched: %q", text)
	}
}

func TestRewriteCodexEnvironmentBodyNoMatchReturnsOriginal(t *testing.T) {
	cases := [][]byte{
		codexEnvBody("就是个普通请求", false),
		[]byte(`{"input":"just a string"}`), // input 非数组
		[]byte(`[1,2,3]`),                   // 根非对象
		[]byte(`{}`),
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for i, body := range cases {
		out, blocks, changed, err := rewriteCodexEnvironmentBody(body, "Asia/Tokyo", now)
		if err != nil {
			t.Fatalf("case %d err=%v", i, err)
		}
		if changed || blocks != 0 {
			t.Fatalf("case %d changed=%v blocks=%d", i, changed, blocks)
		}
		if !bytes.Equal(out, body) {
			t.Fatalf("case %d body mutated: %s", i, out)
		}
	}
}

func TestRewriteCodexEnvironmentBodyPreservesKeyOrder(t *testing.T) {
	// 刻意构造非字母序：root {model,input,instructions,store}，item {type,role,content}，
	// content 元素 {type,text}。改写后所有层级保持原序。
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n<timezone>Europe/Berlin</timezone>\n<current_date>2026-09-27</current_date>\n</environment_context>"}]}],"instructions":"You are Codex","store":false}`)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	out, _, changed, err := rewriteCodexEnvironmentBody(body, "Asia/Tokyo", now)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	s := string(out)
	for _, pair := range [][2]string{
		{`"model":"gpt-5.6-sol"`, `"input":`},
		{`"input":[`, `"type":"message","role":"user","content":[`},
		{`"role":"user"`, `"content":`},
		{`"type":"input_text"`, `"text":"<environment_context>`},
		{`Asia/Tokyo</timezone>`, `</environment_context>`},
	} {
		a, b := strings.Index(s, pair[0]), strings.Index(s, pair[1])
		if a < 0 {
			t.Fatalf("missing %q in %s", pair[0], s)
		}
		if b < 0 {
			t.Fatalf("missing %q in %s", pair[1], s)
		}
		if a >= b {
			t.Fatalf("order broken: %q (at %d) should precede %q (at %d):\n%s", pair[0], a, pair[1], b, s)
		}
	}
	if idx := strings.Index(s, `"instructions"`); idx < strings.Index(s, `"input"`) || idx == -1 {
		t.Fatalf("instructions should come after input: %s", s)
	}
}

func TestRewriteCodexEnvironmentBodyInvalidTZ(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	_, _, _, err := rewriteCodexEnvironmentBody(codexEnvBody(codexEnvSampleBlock, false), "Not/AZone", now)
	if err == nil {
		t.Fatal("expected error for invalid tz")
	}
}

func TestCodexEnvRewriteSettingsValidate(t *testing.T) {
	base := DefaultCodexEnvRewriteSettings()

	s := base
	if err := s.Validate(); err != nil {
		t.Fatalf("default should validate: %v", err)
	}

	s = base
	s.Mode = "fixed"
	if err := s.Validate(); err == nil {
		t.Fatal("bad mode should fail")
	}

	s = base
	s.CustomTZ = "Asia/Shanghai"
	if err := s.Validate(); err == nil {
		t.Fatal("CN tz must not be in whitelist")
	}
	if CodexEnvInTZWhitelist("Asia/Shanghai") || CodexEnvInTZWhitelist("Asia/Hong_Kong") ||
		CodexEnvInTZWhitelist("Asia/Macau") || CodexEnvInTZWhitelist("Asia/Taipei") {
		t.Fatal("mainland CN/HK/MO/TW must be excluded from whitelist")
	}

	s = base
	s.DefaultTZ = "Mars/Olympus"
	if err := s.Validate(); err == nil {
		t.Fatal("bad default_tz should fail")
	}

	s = base
	s.Overrides = map[string]string{"abc": "Asia/Tokyo"}
	if err := s.Validate(); err == nil {
		t.Fatal("non-numeric override key should fail")
	}

	s = base
	s.Overrides = map[string]string{"0": "Asia/Tokyo"}
	if err := s.Validate(); err == nil {
		t.Fatal("non-positive override key should fail")
	}

	s = base
	s.Overrides = map[string]string{"7": "Asia/Shanghai"}
	if err := s.Validate(); err == nil {
		t.Fatal("override value outside whitelist should fail")
	}

	s = base
	s.Overrides = nil
	if err := s.Validate(); err != nil {
		t.Fatalf("nil overrides should normalize: %v", err)
	}
	if s.Overrides == nil {
		t.Fatal("nil overrides should become empty map")
	}

	s = base
	s.EgressCacheHours = 0
	if err := s.Validate(); err != nil {
		t.Fatalf("out-of-range cache hours should clamp: %v", err)
	}
	if s.EgressCacheHours != 24 {
		t.Fatalf("expected clamp to 24, got %d", s.EgressCacheHours)
	}
}

func TestCodexEnvTargetTZ(t *testing.T) {
	repo := newCodexEnvTestRepo()
	svc := NewCodexEnvRewriteService(repo)
	ctx := context.Background()
	cfg := DefaultCodexEnvRewriteSettings()

	// overrides 优先级最高。
	cfg.Mode = "custom"
	cfg.CustomTZ = "Asia/Tokyo"
	cfg.Overrides = map[string]string{"42": "Europe/Paris"}
	if got := svc.TargetTZ(ctx, cfg, 42, ""); got != "Europe/Paris" {
		t.Fatalf("override should win, got %s", got)
	}
	if got := svc.TargetTZ(ctx, cfg, 43, ""); got != "Asia/Tokyo" {
		t.Fatalf("custom mode, got %s", got)
	}

	// 白名单外的 override 值被忽略（走 mode）。
	cfg.Overrides = map[string]string{"42": "Asia/Shanghai"}
	if got := svc.TargetTZ(ctx, cfg, 42, ""); got != "Asia/Tokyo" {
		t.Fatalf("invalid override should fall back to mode, got %s", got)
	}

	// random_stable：同一账号稳定，且落在白名单内。
	cfg.Mode = "random_stable"
	cfg.Overrides = nil
	first := svc.TargetTZ(ctx, cfg, 7, "")
	for i := 0; i < 20; i++ {
		if got := svc.TargetTZ(ctx, cfg, 7, ""); got != first {
			t.Fatalf("random_stable unstable: %s vs %s", got, first)
		}
	}
	if !CodexEnvInTZWhitelist(first) {
		t.Fatalf("random_stable out of whitelist: %s", first)
	}
	// 不同账号分布应不止一个值（30 时区 × 抽样 60 账号）。
	seen := map[string]bool{}
	for id := int64(1); id <= 60; id++ {
		seen[svc.TargetTZ(ctx, cfg, id, "")] = true
	}
	if len(seen) < 2 {
		t.Fatalf("random_stable degenerate distribution: %v", seen)
	}
}

func TestCodexEnvEgressTZCache(t *testing.T) {
	repo := newCodexEnvTestRepo()
	svc := NewCodexEnvRewriteService(repo)
	cfg := DefaultCodexEnvRewriteSettings()
	ctx := context.Background()

	// 正缓存：预填后直接命中，不触发查询。
	svc.egressMu.Lock()
	svc.egressCache["direct"] = codexEnvEgressEntry{tz: "America/New_York", expires: time.Now().Add(time.Hour)}
	svc.egressMu.Unlock()
	if got := svc.TargetTZ(ctx, cfg, 1, ""); got != "America/New_York" {
		t.Fatalf("positive cache hit, got %s", got)
	}

	// 负缓存（"-"）：回退 DefaultTZ。
	svc.egressMu.Lock()
	svc.egressCache["direct"] = codexEnvEgressEntry{tz: "-", expires: time.Now().Add(time.Hour)}
	svc.egressMu.Unlock()
	if got := svc.TargetTZ(ctx, cfg, 1, ""); got != cfg.DefaultTZ {
		t.Fatalf("negative cache should fall back, got %s", got)
	}

	// 白名单外的正缓存同样回退（如出口在 CN）。
	svc.egressMu.Lock()
	svc.egressCache["direct"] = codexEnvEgressEntry{tz: "Asia/Shanghai", expires: time.Now().Add(time.Hour)}
	svc.egressMu.Unlock()
	if got := svc.TargetTZ(ctx, cfg, 1, ""); got != cfg.DefaultTZ {
		t.Fatalf("non-whitelist cache should fall back, got %s", got)
	}
}

func TestCodexEnvEgressTZAsyncFill(t *testing.T) {
	repo := newCodexEnvTestRepo()
	svc := NewCodexEnvRewriteService(repo)
	cfg := DefaultCodexEnvRewriteSettings()
	ctx := context.Background()

	done := make(chan struct{})
	svc.lookupEgressTZ = func(ctx context.Context, proxyURL string) (string, error) {
		if proxyURL != "http://p:1" {
			t.Errorf("unexpected proxy url %q", proxyURL)
		}
		close(done)
		return "Asia/Kuala_Lumpur", nil
	}

	// 未命中：同步返回 DefaultTZ，不阻塞。
	if got := svc.TargetTZ(ctx, cfg, 1, "http://p:1"); got != cfg.DefaultTZ {
		t.Fatalf("cache miss should return default, got %s", got)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("async lookup never ran")
	}
	// 异步填入后（等一小下保证写回完成）命中缓存。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := svc.TargetTZ(ctx, cfg, 1, "http://p:1"); got == "Asia/Kuala_Lumpur" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("async fill did not land in cache")
}

func TestCodexEnvEgressCacheKeyNoLeak(t *testing.T) {
	if got := codexEnvEgressCacheKey(""); got != "direct" {
		t.Fatalf("empty proxy should key 'direct', got %s", got)
	}
	secret := "http://user:supersecret@10.0.0.1:8080"
	key := codexEnvEgressCacheKey(secret)
	if strings.Contains(key, "supersecret") || len(key) != 64 {
		t.Fatalf("proxy secret leaked in cache key: %s", key)
	}
	if key == codexEnvEgressCacheKey("http://other:1") {
		t.Fatal("different proxies must not share cache key")
	}
}

// codexEnvEnabledSvc 开启改写的服务 + 固定时钟。
func codexEnvEnabledSvc(t *testing.T, tz string) (*CodexEnvRewriteService, *codexEnvTestRepo) {
	t.Helper()
	repo := newCodexEnvTestRepo()
	svc := NewCodexEnvRewriteService(repo)
	cfg := DefaultCodexEnvRewriteSettings()
	cfg.Enabled = true
	cfg.Mode = "custom"
	cfg.CustomTZ = tz
	raw, _ := json.Marshal(cfg)
	repo.data[SettingKeyCodexEnvRewrite] = string(raw)
	svc.settingsMu.Lock()
	svc.settingsCache, svc.settingsLoaded = cfg, time.Now()
	svc.settingsMu.Unlock()
	return svc, repo
}

func TestCodexEnvRewriteRequest(t *testing.T) {
	svc, _ := codexEnvEnabledSvc(t, "Asia/Tokyo")
	now := time.Date(2026, 9, 27, 20, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses",
		bytes.NewReader(codexEnvBody(codexEnvSampleBlock, false)))
	account := &Account{ID: 5}
	out := svc.RewriteRequest(req, account)
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Contains(body, []byte("<timezone>Asia/Tokyo</timezone>")) {
		t.Fatalf("rewrite did not apply: %s", body)
	}
	// 可重放：GetBody 能再读一次且内容一致。
	replayed, err := out.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	again, _ := io.ReadAll(replayed)
	if !bytes.Equal(body, again) {
		t.Fatal("GetBody replay mismatch")
	}
	if out.ContentLength != int64(len(body)) {
		t.Fatalf("ContentLength=%d len=%d", out.ContentLength, len(body))
	}
}

func TestCodexEnvRewriteRequestGates(t *testing.T) {
	svc, _ := codexEnvEnabledSvc(t, "Asia/Tokyo")
	account := &Account{ID: 5}

	// 非 POST。
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses",
		bytes.NewReader(codexEnvBody(codexEnvSampleBlock, false)))
	if out := svc.RewriteRequest(req, account); out != req {
		t.Fatal("GET must be returned verbatim")
	}

	// 非 /responses 路径。
	req2, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/models",
		bytes.NewReader(codexEnvBody(codexEnvSampleBlock, false)))
	if out := svc.RewriteRequest(req2, account); out != req2 {
		t.Fatal("non-/responses path must be returned verbatim")
	}

	// 未启用：body 原样且仍可读。
	repo := newCodexEnvTestRepo()
	disabled := NewCodexEnvRewriteService(repo) // 默认 enabled=false
	orig := codexEnvBody(codexEnvSampleBlock, false)
	req3, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses",
		bytes.NewReader(orig))
	out3 := disabled.RewriteRequest(req3, account)
	body, _ := io.ReadAll(out3.Body)
	if !bytes.Equal(body, orig) {
		t.Fatalf("disabled service must not touch body: %s", body)
	}

	// body 无环境块：预检短路，原字节放回。
	noEnv := codexEnvBody("no env here", false)
	req4, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses",
		bytes.NewReader(noEnv))
	body4, _ := io.ReadAll(svc.RewriteRequest(req4, account).Body)
	if !bytes.Equal(body4, noEnv) {
		t.Fatalf("no-env body mutated: %s", body4)
	}

	// nil body / nil 账号安全。
	req5, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	if out := svc.RewriteRequest(req5, account); out != req5 {
		t.Fatal("nil body must be returned verbatim")
	}
	req6, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses",
		bytes.NewReader(codexEnvBody(codexEnvSampleBlock, false)))
	if out := svc.RewriteRequest(req6, nil); out == nil {
		t.Fatal("nil account must not panic")
	}
}

func TestCodexEnvUpdateSettingsRoundTrip(t *testing.T) {
	repo := newCodexEnvTestRepo()
	svc := NewCodexEnvRewriteService(repo)
	ctx := context.Background()

	// 保存 → 内存缓存即时生效。
	cfg := DefaultCodexEnvRewriteSettings()
	cfg.Enabled = true
	cfg.Mode = "random_stable"
	if err := svc.UpdateSettings(ctx, cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := svc.GetSettings(ctx)
	if !got.Enabled || got.Mode != "random_stable" {
		t.Fatalf("cache not refreshed: %+v", got)
	}

	// 非法配置拒绝且不落库。
	bad := cfg
	bad.CustomTZ = "Asia/Shanghai"
	if err := svc.UpdateSettings(ctx, bad); err == nil {
		t.Fatal("invalid settings must be rejected")
	}

	// 新实例（模拟重启/跨副本）从 DB 读回同一配置。
	svc2 := NewCodexEnvRewriteService(repo)
	got2 := svc2.GetSettings(ctx)
	if !got2.Enabled || got2.Mode != "random_stable" {
		t.Fatalf("persisted settings lost: %+v", got2)
	}
}
