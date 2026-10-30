package pricingrules

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// rules.go 根据可版本化的价卡补充规则，选择输入、输出和缓存分项的倍率。
// 普通价卡保存美元基础单价；这里保存报价所要求的输出长度、思考模式和缓存寿命条件。
// 倍率在 Token 分项计算后应用，因此继续使用已选定的上下文价、服务档位和分组倍率。
//
// Facts 从真实上游用量与实际请求中提取，不通过请求的缓存寿命设置推测已写入数量。
// 日历使用北京时间判断放假日期，再由调用方决定是否应用原来的高峰时段。
// 规则文件在进程启动时完整验证，部署者更新文件后重启即可发布一个一致的快照。

// Rules 保存一个已校验的规则快照，发布后供并发结算只读访问。
type Rules struct {
	Version  int                  `json:"version"`        // 配置格式版本
	Models   map[string]ModelRule `json:"models"`         // 规范模型名对应规则
	Holidays map[string][]string  `json:"china_holidays"` // 年份对应北京时间放假日期
}

// ModelRule 记录普通价卡无法表示的附加计费条件，未配置的倍率为一。
type ModelRule struct {
	CacheWrite12hMultiplier     float64      `json:"cache_write_12h_multiplier,omitempty"`     // 相对短期缓存写入倍率
	ExplicitCacheReadMultiplier float64      `json:"explicit_cache_read_multiplier,omitempty"` // 相对隐式缓存读取倍率
	ThinkingOutputMultiplier    float64      `json:"thinking_output_multiplier,omitempty"`     // 相对普通输出倍率
	ThinkingDefault             bool         `json:"thinking_default,omitempty"`               // 缺少模式证据时的官方默认模式
	HolidayOffPeak              bool         `json:"holiday_off_peak,omitempty"`               // 中国放假日期免高峰价
	OutputTiers                 []OutputTier `json:"output_tiers,omitempty"`                   // 首个命中档位优先
}

// OutputTier 描述输入总上下文与输出长度均达到下限时的分项倍率。
type OutputTier struct {
	MinInput   int     `json:"min_input_tokens"`                 // 包含缓存的输入下限
	MinOutput  int     `json:"min_output_tokens"`                // 包含思考的输出下限
	Input      float64 `json:"input_multiplier,omitempty"`       // 普通输入分项倍率
	Output     float64 `json:"output_multiplier,omitempty"`      // 输出分项倍率
	CacheRead  float64 `json:"cache_read_multiplier,omitempty"`  // 缓存读取分项倍率
	CacheWrite float64 `json:"cache_write_multiplier,omitempty"` // 缓存写入分项倍率
}

// Facts 只携带计价所需的计数和模式，不包含请求文本或用户凭据。
type Facts struct {
	CacheWrite12hTokens int    `json:"cache_write_12h_tokens,omitempty"` // 上游报告的十二小时写入明细
	ReasoningTokens     int    `json:"reasoning_tokens,omitempty"`       // 输出中的思考Token子集
	ThinkingMode        string `json:"thinking_mode,omitempty"`          // enabled、disabled或缺省
	ExplicitCache       bool   `json:"explicit_cache,omitempty"`         // 请求带有效显式缓存标记
}

// Factors 是已选定用量的分项倍率，供结算器重新计算总额及实际扣费。
type Factors struct {
	Input      float64 // 普通输入分项倍率
	Output     float64 // 输出分项倍率
	CacheRead  float64 // 缓存读取分项倍率
	CacheWrite float64 // 缓存写入分项倍率
}

// LoadDefault 在启动时读取持久规则文件，缺少默认文件表示未启用。
// 明确配置了路径却无法读取或校验失败时返回错误，调用方应停止启动，防止按残缺规则计费。
func LoadDefault() (*Rules, error) {
	path := strings.TrimSpace(os.Getenv("OFFICIAL_PRICING_RULES_FILE"))
	explicit := path != ""
	if path == "" {
		path = "/app/data/official_pricing_rules.json"
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取计费规则失败: %w", err)
	}
	return Parse(data)
}

// Parse 整体校验规则文件，拒绝未知字段、重复规范模型名、无效日期与异常倍率。
// 成功结果只保存有效状态；倍率零值代表未覆盖，避免把缺省值解释成免费计费。
func Parse(data []byte) (*Rules, error) {
	var rules Rules
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("计费规则必须是单个JSON对象")
	}
	if rules.Version != 1 {
		return nil, fmt.Errorf("不支持的计费规则版本: %d", rules.Version)
	}
	normalized := make(map[string]ModelRule, len(rules.Models))
	for model, rule := range rules.Models {
		key := ModelName(model)
		if key == "" {
			return nil, fmt.Errorf("模型名为空")
		}
		if _, found := normalized[key]; found {
			return nil, fmt.Errorf("模型规则重复: %s", key)
		}
		for _, value := range []float64{rule.CacheWrite12hMultiplier, rule.ExplicitCacheReadMultiplier, rule.ThinkingOutputMultiplier} {
			if err := validFactor(value); err != nil {
				return nil, err
			}
		}
		for _, tier := range rule.OutputTiers {
			if tier.MinInput < 0 || tier.MinOutput < 0 {
				return nil, fmt.Errorf("Token档位下限不能为负")
			}
			for _, value := range []float64{tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite} {
				if err := validFactor(value); err != nil {
					return nil, err
				}
			}
		}
		normalized[key] = rule
	}
	for year, dates := range rules.Holidays {
		if len(year) != 4 || len(dates) == 0 {
			return nil, fmt.Errorf("节假日年份无效: %s", year)
		}
		for _, date := range dates {
			parsed, err := time.Parse("2006-01-02", date)
			if err != nil || parsed.Format("2006") != year {
				return nil, fmt.Errorf("节假日日期无效: %s", date)
			}
		}
	}
	rules.Models = normalized
	return &rules, nil
}

// ModelName 去掉复合Key的分组前缀并统一大小写，使两种请求身份共享同一官价规则。
func ModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(model, "/"); index >= 0 {
		model = model[index+1:]
	}
	return model
}

// validFactor 检查可持久化的倍率范围，零值作为没有覆盖该分项的标记。
func validFactor(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1000 {
		return fmt.Errorf("计费倍率无效: %v", value)
	}
	return nil
}

// Select 根据已观测用量选择首个输出档位，再合并思考与缓存寿命倍率。
// 十二小时明细是总写入量的子集，超出总量时截断，缺失部分仍按短期写入价计算。
// 输出中的思考Token已经包含在输出总数，模式只改变单价，不会重复增加输出数量。
func (rules *Rules) Select(model string, input, output, writes int, facts Facts) Factors {
	result := Factors{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}
	if rules == nil {
		return result
	}
	rule, exists := rules.Models[ModelName(model)]
	if !exists {
		return result
	}
	for _, tier := range rule.OutputTiers {
		if input >= tier.MinInput && output >= tier.MinOutput {
			if tier.Input > 0 {
				result.Input = tier.Input
			}
			if tier.Output > 0 {
				result.Output = tier.Output
			}
			if tier.CacheRead > 0 {
				result.CacheRead = tier.CacheRead
			}
			if tier.CacheWrite > 0 {
				result.CacheWrite = tier.CacheWrite
			}
			break
		}
	}
	thinking := facts.ThinkingMode == "enabled" || facts.ReasoningTokens > 0 || (facts.ThinkingMode == "" && rule.ThinkingDefault)
	if thinking && rule.ThinkingOutputMultiplier > 0 {
		result.Output *= rule.ThinkingOutputMultiplier
	}
	if facts.ExplicitCache && rule.ExplicitCacheReadMultiplier > 0 {
		result.CacheRead *= rule.ExplicitCacheReadMultiplier
	}
	if writes > 0 && rule.CacheWrite12hMultiplier > 0 {
		// 上游TTL明细与总写入量重叠，按加权倍率分摊，避免两次计入同一批Token。
		longWrites := min(max(facts.CacheWrite12hTokens, 0), writes)
		result.CacheWrite *= 1 + float64(longWrites)/float64(writes)*(rule.CacheWrite12hMultiplier-1)
	}
	return result
}

// SkipPeak 判断指定模型是否应免收高峰价，零时刻由调用方先解析为请求时刻。
// 缺少已发布年份的日历时采用低峰价，避免把未知节假日当作工作日多扣费。
func (rules *Rules) SkipPeak(model string, at time.Time) bool {
	if rules == nil || !rules.Models[ModelName(model)].HolidayOffPeak || at.IsZero() {
		return false
	}
	local := at.In(time.FixedZone("Asia/Shanghai", 8*3600))
	dates, known := rules.Holidays[local.Format("2006")]
	if !known {
		return true
	}
	date := local.Format("2006-01-02")
	for _, holiday := range dates {
		if holiday == date {
			return true
		}
	}
	return false
}

// ChinaHoliday2026 返回国务院已发布的2026年放假区间命中状态，使用北京时间日期。
// 日历不把补班周末变为高峰，峰谷规则中的周末豁免仍由原计费器单独执行。
func ChinaHoliday2026(at time.Time) bool {
	date := at.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	periods := [][2]string{{"2026-01-01", "2026-01-03"}, {"2026-02-15", "2026-02-23"}, {"2026-04-04", "2026-04-06"}, {"2026-05-01", "2026-05-05"}, {"2026-06-19", "2026-06-21"}, {"2026-09-25", "2026-09-27"}, {"2026-10-01", "2026-10-07"}}
	for _, period := range periods {
		if date >= period[0] && date <= period[1] {
			return true
		}
	}
	return false
}

// UsageFacts 读取上游标准用量对象中的TTL和思考子集，嵌套输入明细优先于兼容别名。
// 缺少明细时保留零值，由价卡按短期写入计费；不根据输入长度猜测缓存命中。
func UsageFacts(value gjson.Result) Facts {
	return Facts{
		CacheWrite12hTokens: max(int(first(value, "input_tokens_details.cache_write_tokens_details.ttl_12h_tokens", "prompt_tokens_details.cache_write_tokens_details.ttl_12h_tokens", "pricing_details.cache_write_12h_tokens")), 0),
		ReasoningTokens:     max(int(first(value, "output_tokens_details.reasoning_tokens", "completion_tokens_details.reasoning_tokens", "pricing_details.reasoning_tokens")), 0),
	}
}

// first 返回首个实际存在的计数，包括显式零值，防止兼容字段覆盖权威明细。
func first(value gjson.Result, paths ...string) int64 {
	for _, path := range paths {
		if v := value.Get(path); v.Exists() {
			return v.Int()
		}
	}
	return 0
}

// RequestFacts 读取请求的模式开关和支持位置的显式缓存标记，不持有请求字节。
// 任意文本或metadata中的同名键不能触发缓存优惠；只检查协议支持的内容块和工具。
func RequestFacts(body []byte) Facts {
	value := gjson.ParseBytes(body)
	facts := Facts{}
	thinking := value.Get("enable_thinking")
	if !thinking.Exists() {
		thinking = value.Get("chat_template_kwargs.enable_thinking")
	}
	if thinking.Exists() {
		if thinking.Bool() {
			facts.ThinkingMode = "enabled"
		} else {
			facts.ThinkingMode = "disabled"
		}
	}
	if thinking := value.Get("thinking.type"); thinking.Exists() {
		facts.ThinkingMode = thinking.String()
	}
	if value.Get("cache_control.type").String() == "ephemeral" {
		facts.ExplicitCache = true
	}
	for _, path := range []string{"messages", "input", "system", "tools"} {
		for _, item := range value.Get(path).Array() {
			if item.Get("cache_control.type").String() == "ephemeral" {
				facts.ExplicitCache = true
			}
			for _, block := range item.Get("content").Array() {
				if block.Get("cache_control.type").String() == "ephemeral" {
					facts.ExplicitCache = true
				}
			}
		}
	}
	return facts
}

// MergeRequest 把已发送请求的模式并入上游计费观察，保留上游报告的TTL和思考数量。
func (facts *Facts) MergeRequest(request Facts) {
	facts.ExplicitCache = request.ExplicitCache
	facts.ThinkingMode = request.ThinkingMode
}
