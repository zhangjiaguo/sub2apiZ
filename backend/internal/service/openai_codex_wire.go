package service

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/tidwall/gjson"
)

// codexWire 模式：转发路径的手工 HTTP/1.1 线写出器接入。
//
// 37a278a45 已把会话方言对齐 codex-rs 0.157.1（连字符三件套等），但传输层
// 仍是 Go net/http：规范大写驼峰头 + 字母序发送 + 透明补 Accept-Encoding:
// gzip + map 序列化的字母序 JSON 体。真实 codex CLI（reqwest/hyper）是全小写
// 头、固定插入序、无 accept-encoding、serde 结构体字段序。本文件在
// dispatchOpenAIUpstream 的 TLS 指纹分支上按条件打 WithHTTPUpstreamCodexWire
// 标记，并把 /responses 请求体重排为 codex 字段序；repository 层据标记改用
// tlsfingerprint.WireH1Transport（TLS 层不变，HTTP 层逐字节对齐）。
//
// 保险丝：SUB2API_CODEX_WIRE=off 时整体关闭（重建容器即软关闭）。

// codexWireDisabled 解析保险丝环境变量（进程级缓存）。
var codexWireDisabled = sync.OnceValue(func() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SUB2API_CODEX_WIRE")), "off")
})

// isBuiltInCodexTLSProfile 判断 profile 是否为内置 Codex CLI 指纹。wire 写出
// 器只发 HTTP/1.1 无 ALPN 线型，与内置 codex 指纹（OpenSSL 3.6、no-ALPN H1）
// 严格配套；自定义 DB 指纹可能协商 H2，手工 H1 客户端会与 ALPN 结果矛盾，
// 因此仅内置 codex 指纹启用。
func isBuiltInCodexTLSProfile(profile *tlsfingerprint.Profile) bool {
	if profile == nil {
		return false
	}
	return profile == tlsfingerprint.CodexProfile ||
		strings.HasPrefix(profile.Name, "Built-in Codex CLI")
}

// prepareCodexWireRequest 对命中 wire 模式的请求打标记并重排请求体：
//   - context 打 WithHTTPUpstreamCodexWire（repository 层据此选 wire transport）；
//   - POST /responses 的 JSON 体重排为 codex ResponsesApiRequest 字段序。
//
// 调用方须保证仅在非 warmPool（打票出口覆盖）路径调用：覆盖路径刻意保持
// 每请求新连接 = 新出口，keep-alive 池会破坏该语义。
func prepareCodexWireRequest(req *http.Request) *http.Request {
	req = req.WithContext(WithHTTPUpstreamCodexWire(req.Context()))
	// 仅 /responses 本路径是 ResponsesApiRequest 形状；子路径（input_tokens
	// 等）与 count_tokens 的 body 结构不同，不重排。HasSuffix 同时排除
	// "/responses/input_tokens" 这类带后缀的路径。
	if req.Method != http.MethodPost || req.Body == nil ||
		!strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/responses") {
		return req
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		restoreReplayableBody(req, body)
		return req
	}
	reordered := reorderCodexResponsesBodyForWire(body)
	if reordered == nil {
		restoreReplayableBody(req, body)
		return req
	}
	restoreReplayableBody(req, reordered)
	return req
}

// restoreReplayableBody 把（重排或原始的）body 以可重放形式放回请求：
// Body/GetBody/ContentLength 三者一致，后续 403 重试与 failover 仍可重发。
func restoreReplayableBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}

// codexWireResponsesBodyOrder 是 codex-rs 0.157.1 ResponsesApiRequest 的
// serde 结构体序列化字段序（skip_serializing 的字段缺失即跳过）。
var codexWireResponsesBodyOrder = []string{
	"model",
	"instructions",
	"input",
	"tools",
	"tool_choice",
	"parallel_tool_calls",
	"reasoning",
	"store",
	"stream",
	"stream_options",
	"include",
	"service_tier",
	"prompt_cache_key",
	"text",
	"client_metadata",
	"access_programs",
}

// codexWireReasoningOrder 是 reasoning 对象的字段序（effort, summary, context）。
var codexWireReasoningOrder = []string{"effort", "summary", "context"}

// reorderCodexResponsesBodyForWire 把 /responses 请求体顶层字段重排为 codex
// 字段序。所有字段值原样保留（raw 段拷贝，不改写转义/数字格式/未知字段）；
// 顶层之外的调整仅两类：reasoning 子对象按 codex 字段序、input/tools 内的
// 对象 type 字段前置（serde 枚举序列化形态）。非法 JSON / 非对象返回 nil
// （调用方保留原 body）。
//
// client_metadata 在真实 codex 里是 HashMap<String, String>（随机序），任何
// 顺序都真实，无需处理。
func reorderCodexResponsesBodyForWire(body []byte) []byte {
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return nil
	}
	var fields []string
	raws := map[string]string{}
	root.ForEach(func(key, value gjson.Result) bool {
		fields = append(fields, key.String())
		raws[key.String()] = value.Raw
		return true
	})
	if len(fields) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emit := func(key, raw string) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		writeJSONString(&buf, key)
		buf.WriteByte(':')
		buf.WriteString(raw)
	}
	emitted := make(map[string]bool, len(fields))
	for _, name := range codexWireResponsesBodyOrder {
		raw, ok := raws[name]
		if !ok {
			continue
		}
		if name == "reasoning" {
			raw = reorderCodexReasoningForWire(raw)
		} else if name == "input" || name == "tools" {
			raw = reorderCodexTypeFirstForWire(raw)
		}
		emit(name, raw)
		emitted[name] = true
	}
	// 未知/扩展字段按原出现顺序补尾（真实 codex 不会发出这些字段；保留
	// 原序最忠实于客户端意图）。
	for _, name := range fields {
		if emitted[name] {
			continue
		}
		emit(name, raws[name])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// reorderCodexReasoningForWire 重排 reasoning 子对象（effort, summary, context
// + 未知字段原序补尾）。非对象原样返回。
func reorderCodexReasoningForWire(raw string) string {
	root := gjson.Parse(raw)
	if !root.IsObject() {
		return raw
	}
	var order []string
	raws := map[string]string{}
	root.ForEach(func(key, value gjson.Result) bool {
		order = append(order, key.String())
		raws[key.String()] = value.Raw
		return true
	})
	if len(order) == 0 {
		return raw
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emit := func(name string) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		writeJSONString(&buf, name)
		buf.WriteByte(':')
		buf.WriteString(raws[name])
	}
	emitted := make(map[string]bool, len(order))
	for _, name := range codexWireReasoningOrder {
		if _, ok := raws[name]; ok {
			emit(name)
			emitted[name] = true
		}
	}
	for _, name := range order {
		if !emitted[name] {
			emit(name)
		}
	}
	buf.WriteByte('}')
	return buf.String()
}

// reorderCodexTypeFirstForWire 递归重排 input/tools 的 JSON 树：所有对象的
// "type" 字段（若存在）前置，其余字段保持原相对顺序；数组与嵌套结构递归
// 处理；叶子值原样输出（raw 拷贝）。这是 serde 枚举（#[serde(tag = "type")]）
// 的序列化形态：tag 字段永远在首位。非法结构原样返回。
func reorderCodexTypeFirstForWire(raw string) string {
	root := gjson.Parse(raw)
	if !root.IsObject() && !root.IsArray() {
		return raw
	}
	var buf bytes.Buffer
	writeCodexWireValue(&buf, root)
	return buf.String()
}

func writeCodexWireValue(buf *bytes.Buffer, v gjson.Result) {
	switch {
	case v.IsArray():
		buf.WriteByte('[')
		first := true
		v.ForEach(func(_, elem gjson.Result) bool {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			writeCodexWireValue(buf, elem)
			return true
		})
		buf.WriteByte(']')
	case v.IsObject():
		// type 字段前置，其余按原序；字段值递归（嵌套数组/对象同样处理）。
		var typeRaw string
		hasType := false
		if t := v.Get("type"); t.Exists() {
			typeRaw = t.Raw
			hasType = true
		}
		buf.WriteByte('{')
		first := true
		emit := func(key string, value gjson.Result) {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			writeJSONString(buf, key)
			buf.WriteByte(':')
			writeCodexWireValue(buf, value)
		}
		if hasType {
			buf.WriteString(`"type":`)
			buf.WriteString(typeRaw)
			first = false
		}
		v.ForEach(func(key, value gjson.Result) bool {
			if hasType && key.String() == "type" {
				return true
			}
			emit(key.String(), value)
			return true
		})
		buf.WriteByte('}')
	default:
		buf.WriteString(v.Raw)
	}
}
