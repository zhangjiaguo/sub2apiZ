package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func TestReorderCodexResponsesBodyForWire(t *testing.T) {
	// 字母序输入（map 序列化的典型形态）→ codex 字段序输出。
	in := `{"stream":true,"reasoning":{"summary":"auto","effort":"medium"},"model":"gpt-6-astra",` +
		`"input":[{"role":"user","type":"message","content":"hi"}],"store":false,` +
		`"instructions":"You are Codex","prompt_cache_key":"sess-1","include":["reasoning.encrypted_content"],` +
		`"custom_extra":{"b":1},"parallel_tool_calls":false,"tool_choice":"auto"}`
	got := reorderCodexResponsesBodyForWire([]byte(in))
	want := `{"model":"gpt-6-astra","instructions":"You are Codex",` +
		`"input":[{"type":"message","role":"user","content":"hi"}],` +
		`"tool_choice":"auto","parallel_tool_calls":false,` +
		`"reasoning":{"effort":"medium","summary":"auto"},"store":false,"stream":true,` +
		`"include":["reasoning.encrypted_content"],"prompt_cache_key":"sess-1","custom_extra":{"b":1}}`
	if string(got) != want {
		t.Fatalf("reordered body mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestReorderCodexResponsesBodyInputTypeFirstRecursive(t *testing.T) {
	// input 深层嵌套的对象全部 type 前置（serde 枚举形态），数组/叶子原样。
	in := `{"model":"m","input":[{"content":[{"text":"hi","type":"input_text"}],"role":"user","type":"message"},` +
		`{"output":"done","call_id":"call_1","type":"function_call_output"}]}`
	got := string(reorderCodexResponsesBodyForWire([]byte(in)))
	// type 前置，其余字段保持文档原序（content 在 role 前）。
	want := `{"model":"m","input":[{"type":"message","content":[{"type":"input_text","text":"hi"}],"role":"user"},` +
		`{"type":"function_call_output","output":"done","call_id":"call_1"}]}`
	if got != want {
		t.Fatalf("input walk mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestReorderCodexResponsesBodyInvalid(t *testing.T) {
	cases := []string{`not json`, `[1,2]`, `{}`, `null`, `123`}
	for _, in := range cases {
		if got := reorderCodexResponsesBodyForWire([]byte(in)); got != nil {
			t.Fatalf("reorder(%q) = %q, want nil", in, got)
		}
	}
}

func TestReorderCodexResponsesBodyPreservesUnknownEscapes(t *testing.T) {
	// 原始编码（转义、数字精度）原样保留，只动顺序。
	in := `{"stream":true,"instructions":"a\"b\\c","temperature":0.500,"model":"m"}`
	got := string(reorderCodexResponsesBodyForWire([]byte(in)))
	want := `{"model":"m","instructions":"a\"b\\c","stream":true,"temperature":0.500}`
	if got != want {
		t.Fatalf("raw preservation mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestPrepareCodexWireRequest(t *testing.T) {
	newReq := func(method, path string, body string) *http.Request {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, "https://chatgpt.com"+path, rd)
		return req
	}

	// POST /responses：打标记 + 重排 body + 可重放。
	req := newReq("POST", "/backend-api/codex/responses",
		`{"stream":true,"model":"m"}`)
	out := prepareCodexWireRequest(req)
	if !HTTPUpstreamCodexWire(out.Context()) {
		t.Fatalf("codex wire flag not set")
	}
	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(b) != `{"model":"m","stream":true}` {
		t.Fatalf("body = %s", b)
	}
	if out.ContentLength != int64(len(b)) {
		t.Fatalf("content length = %d, want %d", out.ContentLength, len(b))
	}
	if out.GetBody == nil {
		t.Fatalf("GetBody must be replayable")
	}
	rb, _ := out.GetBody()
	rbBytes, _ := io.ReadAll(rb)
	if !bytes.Equal(rbBytes, b) {
		t.Fatalf("GetBody replay mismatch")
	}

	// /responses 子路径不重排（input_tokens 等 body 形状不同）。
	req = newReq("POST", "/backend-api/codex/responses/input_tokens", `{"a":1,"b":2}`)
	out = prepareCodexWireRequest(req)
	if !HTTPUpstreamCodexWire(out.Context()) {
		t.Fatalf("flag should still be set for wire transport")
	}
	b, _ = io.ReadAll(out.Body)
	if string(b) != `{"a":1,"b":2}` {
		t.Fatalf("subpath body must not reorder: %s", b)
	}

	// 非 /responses 路径不重排。
	req = newReq("POST", "/backend-api/codex/count_tokens", `{"b":2,"a":1}`)
	out = prepareCodexWireRequest(req)
	b, _ = io.ReadAll(out.Body)
	if string(b) != `{"b":2,"a":1}` {
		t.Fatalf("count_tokens body must not reorder: %s", b)
	}

	// GET 不重排（无 body）。
	req = newReq("GET", "/backend-api/codex/responses", "")
	out = prepareCodexWireRequest(req)
	if !HTTPUpstreamCodexWire(out.Context()) {
		t.Fatalf("GET should still get wire transport flag")
	}

	// 非法 JSON body：原样保留、标记仍然打上。
	req = newReq("POST", "/backend-api/codex/responses", `{invalid`)
	out = prepareCodexWireRequest(req)
	b, _ = io.ReadAll(out.Body)
	if string(b) != `{invalid` {
		t.Fatalf("invalid JSON must pass through: %s", b)
	}
	if !HTTPUpstreamCodexWire(out.Context()) {
		t.Fatalf("flag must be set even with passthrough body")
	}
}

func TestIsBuiltInCodexTLSProfile(t *testing.T) {
	if !isBuiltInCodexTLSProfile(tlsfingerprint.CodexProfile) {
		t.Fatalf("built-in codex profile must match")
	}
	if !isBuiltInCodexTLSProfile(&tlsfingerprint.Profile{Name: "Built-in Codex CLI (OpenSSL 3.6, codex-cli 0.157.1)"}) {
		t.Fatalf("name-prefix profile must match")
	}
	if isBuiltInCodexTLSProfile(&tlsfingerprint.Profile{Name: "Custom Chrome"}) {
		t.Fatalf("custom profile must not match")
	}
	if isBuiltInCodexTLSProfile(nil) {
		t.Fatalf("nil must not match")
	}
}
