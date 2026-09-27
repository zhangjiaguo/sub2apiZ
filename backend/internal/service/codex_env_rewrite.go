package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "time/tzdata" // 内嵌 tz 数据库：LoadLocation 不依赖宿主系统 zoneinfo

	"github.com/tidwall/gjson"
)

// codex_env_rewrite：Codex 出站请求的 <environment_context> 时区/日期改写。
//
// 背景：codex CLI 会在首轮请求的 input 里注入 <environment_context> 块（含
// <timezone> 与 <current_date>），上游由此推断用户所在地。中转出站若与请求
// 声明的时区不一致（例如请求写着 Asia/Shanghai、出口却在海外），是显著的风控
// 破绽。本文件在 doOpenAIUpstream 收口处按配置改写出站 body：
//   - mode=custom         所有账号统一用 custom_tz；
//   - mode=random_stable  每账号按 id 哈希稳定映射到白名单内的一个时区；
//   - mode=egress_ip      按账号出口 IP 查 ipinfo.io 得真实时区（带缓存），
//                         查询失败/不在白名单回退 default_tz；
//   - overrides           按账号 id 精确覆盖（优先级最高）。
//
// 三条不变量：① 改写是纯函数 + fail-open——任何错误原样转发原 body；② 改写后
// body 以可重放形式放回（403 换出口重试、failover 仍可重发）；③ 白名单排除
// 中国大陆及港澳台时区。默认整体关闭（enabled=false），零影响上线。
//
// 已知范围（v1）：仅覆盖 HTTP 转发全家族（doOpenAIUpstream 收口）；WS 直连
// 透传的首帧 body 不在改写范围。

// SettingKeyCodexEnvRewrite 环境改写配置（JSON）的设置键。
const SettingKeyCodexEnvRewrite = "codex_env_rewrite"

// codexEnvRewriteSettingsCacheTTL 配置缓存时长（与打票一致，DB 直改 30s 内生效）。
const codexEnvRewriteSettingsCacheTTL = 30 * time.Second

// CodexEnvTZWhitelist 可选时区白名单（排除中国大陆及港澳台）。
var CodexEnvTZWhitelist = []string{
	"Africa/Cairo", "Africa/Johannesburg", "America/Argentina/Buenos_Aires",
	"America/Chicago", "America/Denver", "America/Los_Angeles", "America/Mexico_City",
	"America/New_York", "America/Sao_Paulo", "America/Toronto", "America/Vancouver",
	"Asia/Bangkok", "Asia/Dubai", "Asia/Ho_Chi_Minh", "Asia/Jakarta", "Asia/Kolkata",
	"Asia/Kuala_Lumpur", "Asia/Manila", "Asia/Seoul", "Asia/Singapore", "Asia/Tokyo",
	"Australia/Perth", "Australia/Sydney", "Europe/Berlin", "Europe/Istanbul",
	"Europe/London", "Europe/Moscow", "Europe/Paris", "Pacific/Auckland", "Pacific/Honolulu",
}

// codexEnvTZWhitelistSet 供 O(1) 白名单判定。
var codexEnvTZWhitelistSet = func() map[string]bool {
	m := make(map[string]bool, len(CodexEnvTZWhitelist))
	for _, tz := range CodexEnvTZWhitelist {
		m[tz] = true
	}
	return m
}()

// CodexEnvInTZWhitelist 判断时区是否在白名单内。
func CodexEnvInTZWhitelist(tz string) bool { return codexEnvTZWhitelistSet[tz] }

// CodexEnvRewriteSettings 环境改写配置。
type CodexEnvRewriteSettings struct {
	Enabled          bool              `json:"enabled"`
	Mode             string            `json:"mode"` // custom | random_stable | egress_ip
	CustomTZ         string            `json:"custom_tz"`
	DefaultTZ        string            `json:"default_tz"`
	EgressCacheHours int               `json:"egress_cache_hours"`
	Overrides        map[string]string `json:"overrides"` // 十进制账号 id → IANA 时区
}

// DefaultCodexEnvRewriteSettings 默认配置（关闭）。
func DefaultCodexEnvRewriteSettings() CodexEnvRewriteSettings {
	return CodexEnvRewriteSettings{
		Enabled:          false,
		Mode:             "egress_ip",
		CustomTZ:         "Asia/Singapore",
		DefaultTZ:        "Asia/Singapore",
		EgressCacheHours: 24,
		Overrides:        map[string]string{},
	}
}

// Validate 校验并归一化配置。
func (c *CodexEnvRewriteSettings) Validate() error {
	switch c.Mode {
	case "custom", "random_stable", "egress_ip":
	default:
		return fmt.Errorf("mode 必须是 custom/random_stable/egress_ip，当前 %q", c.Mode)
	}
	if !CodexEnvInTZWhitelist(c.CustomTZ) {
		return fmt.Errorf("custom_tz %q 不在时区白名单内", c.CustomTZ)
	}
	if !CodexEnvInTZWhitelist(c.DefaultTZ) {
		return fmt.Errorf("default_tz %q 不在时区白名单内", c.DefaultTZ)
	}
	if c.EgressCacheHours < 1 || c.EgressCacheHours > 720 {
		c.EgressCacheHours = 24
	}
	for k, v := range c.Overrides {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("overrides 键 %q 必须是正整数账号 id", k)
		}
		if !CodexEnvInTZWhitelist(v) {
			return fmt.Errorf("overrides[%s]=%q 不在时区白名单内", k, v)
		}
	}
	if c.Overrides == nil {
		c.Overrides = map[string]string{}
	}
	return nil
}

// CodexEnvRewriteService 环境改写服务：配置缓存 + 目标时区解析 + 请求改写。
type CodexEnvRewriteService struct {
	settingRepo SettingRepository
	now         func() time.Time

	settingsMu     sync.RWMutex
	settingsCache  CodexEnvRewriteSettings
	settingsLoaded time.Time

	egressMu    sync.Mutex
	egressCache map[string]codexEnvEgressEntry // key: "direct" 或 sha256hex(proxyURL)
	egressBusy  map[string]bool                // 单飞：同一出口只发一次查询

	// lookupEgressTZ 可注入的出口时区查询（测试用）；nil 时用 ipinfo.io。
	lookupEgressTZ func(ctx context.Context, proxyURL string) (string, error)
}

type codexEnvEgressEntry struct {
	tz      string // 解析出的 IANA 时区（不在白名单也原样缓存，使用时再回退）；"-" 为负缓存
	expires time.Time
}

// NewCodexEnvRewriteService 构造环境改写服务。
func NewCodexEnvRewriteService(settingRepo SettingRepository) *CodexEnvRewriteService {
	return &CodexEnvRewriteService{
		settingRepo: settingRepo,
		now:         time.Now,
		egressCache: make(map[string]codexEnvEgressEntry),
		egressBusy:  make(map[string]bool),
	}
}

// loadSettings 读取配置（带缓存；PUT 后主动失效）。
func (s *CodexEnvRewriteService) loadSettings(ctx context.Context) CodexEnvRewriteSettings {
	s.settingsMu.RLock()
	if !s.settingsLoaded.IsZero() && s.now().Sub(s.settingsLoaded) < codexEnvRewriteSettingsCacheTTL {
		cached := s.settingsCache
		s.settingsMu.RUnlock()
		return cached
	}
	s.settingsMu.RUnlock()

	settings := DefaultCodexEnvRewriteSettings()
	if s.settingRepo != nil {
		if raw, err := s.settingRepo.GetValue(ctx, SettingKeyCodexEnvRewrite); err == nil && strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &settings); err != nil {
				slog.Warn("codex_env_rewrite settings decode failed, using defaults", "error", err)
				settings = DefaultCodexEnvRewriteSettings()
			}
		}
	}
	_ = settings.Validate()

	s.settingsMu.Lock()
	s.settingsCache, s.settingsLoaded = settings, s.now()
	s.settingsMu.Unlock()
	return settings
}

// GetSettings 返回当前配置。
func (s *CodexEnvRewriteService) GetSettings(ctx context.Context) CodexEnvRewriteSettings {
	return s.loadSettings(ctx)
}

// UpdateSettings 校验并保存配置。
func (s *CodexEnvRewriteService) UpdateSettings(ctx context.Context, settings CodexEnvRewriteSettings) error {
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
	if err := s.settingRepo.Set(ctx, SettingKeyCodexEnvRewrite, string(raw)); err != nil {
		return fmt.Errorf("save codex env rewrite settings: %w", err)
	}
	s.settingsMu.Lock()
	s.settingsCache, s.settingsLoaded = settings, s.now()
	s.settingsMu.Unlock()
	return nil
}

// codexEnvEgressCacheKey 出口缓存键：直连为 "direct"，代理为 sha256hex(URL)
// （代理 URL 含密码，不能直接做键）。
func codexEnvEgressCacheKey(proxyURL string) string {
	if proxyURL == "" {
		return "direct"
	}
	sum := sha256.Sum256([]byte(proxyURL))
	return hex.EncodeToString(sum[:])
}

// TargetTZ 解析账号的目标时区（永不为空，失败回退 DefaultTZ）。
// overrides > mode；egress_ip 未命中缓存时返回 DefaultTZ 并异步补查（不阻塞请求）。
func (s *CodexEnvRewriteService) TargetTZ(ctx context.Context, cfg CodexEnvRewriteSettings, accountID int64, proxyURL string) string {
	if tz, ok := cfg.Overrides[strconv.FormatInt(accountID, 10)]; ok && CodexEnvInTZWhitelist(tz) {
		return tz
	}
	switch cfg.Mode {
	case "custom":
		if CodexEnvInTZWhitelist(cfg.CustomTZ) {
			return cfg.CustomTZ
		}
	case "random_stable":
		h := fnv.New32a()
		_, _ = h.Write([]byte(strconv.FormatInt(accountID, 10)))
		return CodexEnvTZWhitelist[h.Sum32()%uint32(len(CodexEnvTZWhitelist))]
	case "egress_ip":
		return s.egressTZ(ctx, cfg, proxyURL)
	}
	return cfg.DefaultTZ
}

// egressTZ 出口 IP 时区（带缓存；未命中返回 DefaultTZ 并单飞异步补查）。
func (s *CodexEnvRewriteService) egressTZ(ctx context.Context, cfg CodexEnvRewriteSettings, proxyURL string) string {
	key := codexEnvEgressCacheKey(proxyURL)
	now := s.now()
	ttl := time.Duration(cfg.EgressCacheHours) * time.Hour
	negativeTTL := 10 * time.Minute

	s.egressMu.Lock()
	if entry, ok := s.egressCache[key]; ok && now.Before(entry.expires) {
		s.egressMu.Unlock()
		if entry.tz == "-" || !CodexEnvInTZWhitelist(entry.tz) {
			return cfg.DefaultTZ
		}
		return entry.tz
	}
	busy := s.egressBusy[key]
	if !busy {
		s.egressBusy[key] = true
	}
	s.egressMu.Unlock()

	if !busy {
		go func() {
			defer func() {
				s.egressMu.Lock()
				delete(s.egressBusy, key)
				s.egressMu.Unlock()
			}()
			tz, err := s.lookupEgress(ctx, proxyURL)
			entry := codexEnvEgressEntry{tz: "-", expires: s.now().Add(negativeTTL)}
			if err == nil && tz != "" {
				entry = codexEnvEgressEntry{tz: tz, expires: s.now().Add(ttl)}
			}
			s.egressMu.Lock()
			s.egressCache[key] = entry
			s.egressMu.Unlock()
		}()
	}
	return cfg.DefaultTZ
}

// lookupEgress 经代理查询出口 IP 的时区（ipinfo.io /json 的 timezone 字段）。
func (s *CodexEnvRewriteService) lookupEgress(ctx context.Context, proxyURL string) (string, error) {
	if s.lookupEgressTZ != nil {
		return s.lookupEgressTZ(ctx, proxyURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipinfo.io/json", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return "", err
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(pu)}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ipinfo status %d", resp.StatusCode)
	}
	var payload struct {
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Timezone == "" {
		return "", errors.New("ipinfo timezone empty")
	}
	return payload.Timezone, nil
}

// RewriteRequest 按配置改写出站请求的 <environment_context>（fail-open，永不改路径/头）。
// 仅处理 POST 且路径以 /responses 结尾的请求；其余原样返回。
func (s *CodexEnvRewriteService) RewriteRequest(req *http.Request, account *Account) *http.Request {
	if s == nil || req == nil || req.Body == nil {
		return req
	}
	settings := s.loadSettings(req.Context())
	if !settings.Enabled || req.Method != http.MethodPost {
		return req
	}
	if !strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/responses") {
		return req
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		restoreReplayableBody(req, body)
		return req
	}
	// 便宜预检：原生 codex 的裸 < 与宿主桥接重编的转义 < 两种
	// 形态都不在，直接放回原 body，不做 JSON 解码。
	if !bytes.Contains(body, []byte("<environment_context")) &&
		!bytes.Contains(body, []byte("\\u003cenvironment_context")) {
		restoreReplayableBody(req, body)
		return req
	}
	proxyURL := ""
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	target := s.TargetTZ(req.Context(), settings, accountID, proxyURL)
	out, _, changed, err := rewriteCodexEnvironmentBody(body, target, s.now())
	if err != nil || !changed {
		restoreReplayableBody(req, body)
		return req
	}
	restoreReplayableBody(req, out)
	return req
}

// codexEnvTimezoneTagRe / codexEnvDateTagRe 环境块内的首个标签匹配。
var (
	codexEnvTimezoneTagRe = regexp.MustCompile(`<timezone>[^<]*</timezone>`)
	codexEnvDateTagRe     = regexp.MustCompile(`<current_date>\d{4}-\d{2}-\d{2}</current_date>`)
)

// rewriteCodexEnvironmentBody 纯函数：改写 body 内全部 <environment_context> 块的
// <timezone> 与 <current_date>（首个标签；自闭合日期不动）。返回 (新 body, 命中块
// 数, 是否有改动, 错误)。无改动时返回原 body；目标时区非法返回 err（调用方
// fail-open）。
//
// 用 gjson 逐层重建（字段保持文档原序——map 重编会字母序打乱键序，破坏与真实
// codex 的线型一致性）；只替换改动过的 text/content/input 值，其余字段 raw 拷贝。
// 输入是宿主桥接重编过的转义形态（<）也没问题：gjson 解码后按文本前缀匹配。
func rewriteCodexEnvironmentBody(body []byte, targetTZ string, now time.Time) ([]byte, int, bool, error) {
	loc, err := time.LoadLocation(targetTZ)
	if err != nil {
		return nil, 0, false, fmt.Errorf("load location %s: %w", targetTZ, err)
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return body, 0, false, nil
	}
	input := root.Get("input")
	if !input.Exists() || !input.IsArray() {
		return body, 0, false, nil
	}

	blocks := 0
	changed := false

	// 重建 content 数组（保序；replaceKey 非空时用 replaceRaw 替换该键的值）。
	rebuildObject := func(obj gjson.Result, replaceKey string, replaceRaw string) []byte {
		var buf bytes.Buffer
		buf.WriteByte('{')
		first := true
		obj.ForEach(func(key, value gjson.Result) bool {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			writeJSONString(&buf, key.String())
			buf.WriteByte(':')
			if key.String() == replaceKey {
				buf.WriteString(replaceRaw)
			} else {
				buf.WriteString(value.Raw)
			}
			return true
		})
		buf.WriteByte('}')
		return buf.Bytes()
	}

	var newInput bytes.Buffer
	newInput.WriteByte('[')
	itemFirst := true
	input.ForEach(func(_, item gjson.Result) bool {
		if !itemFirst {
			newInput.WriteByte(',')
		}
		itemFirst = false
		if !item.IsObject() || item.Get("role").String() != "user" {
			newInput.WriteString(item.Raw)
			return true
		}
		content := item.Get("content")
		if !content.Exists() || !content.IsArray() {
			newInput.WriteString(item.Raw)
			return true
		}
		contentChanged := false
		var newContent bytes.Buffer
		newContent.WriteByte('[')
		cFirst := true
		content.ForEach(func(_, c gjson.Result) bool {
			if !cFirst {
				newContent.WriteByte(',')
			}
			cFirst = false
			if !c.IsObject() || c.Get("type").String() != "input_text" ||
				!strings.HasPrefix(strings.TrimSpace(c.Get("text").String()), "<environment_context>") {
				newContent.WriteString(c.Raw)
				return true
			}
			newText, ok := rewriteCodexEnvBlock(c.Get("text").String(), targetTZ, loc, now)
			if !ok {
				newContent.WriteString(c.Raw)
				return true
			}
			blocks++
			contentChanged = true
			textRaw, err := codexEnvMarshalNoEscape(newText)
			if err != nil {
				newContent.WriteString(c.Raw)
				return true
			}
			newContent.Write(rebuildObject(c, "text", string(textRaw)))
			return true
		})
		newContent.WriteByte(']')
		if !contentChanged {
			newInput.WriteString(item.Raw)
			return true
		}
		changed = true
		newInput.Write(rebuildObject(item, "content", newContent.String()))
		return true
	})
	newInput.WriteByte(']')

	if !changed {
		return body, blocks, false, nil
	}
	return rebuildObject(root, "input", newInput.String()), blocks, true, nil
}

// rewriteCodexEnvBlock 改写单个环境块文本：首个 <timezone> 标签替换为目标时区、
// 首个 <current_date> 日期标签替换为目标时区的今天；自闭合日期不动。
func rewriteCodexEnvBlock(text, targetTZ string, loc *time.Location, now time.Time) (string, bool) {
	changed := false
	if m := codexEnvTimezoneTagRe.FindStringIndex(text); m != nil {
		text = text[:m[0]] + "<timezone>" + targetTZ + "</timezone>" + text[m[1]:]
		changed = true
	}
	if m := codexEnvDateTagRe.FindStringIndex(text); m != nil {
		dateStr := now.In(loc).Format("2006-01-02")
		text = text[:m[0]] + "<current_date>" + dateStr + "</current_date>" + text[m[1]:]
		changed = true
	}
	return text, changed
}

// codexEnvMarshalNoEscape 用 SetEscapeHTML(false) 编码字符串（保持 < > 原形）。
func codexEnvMarshalNoEscape(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
