package tlsfingerprint

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 手工 HTTP/1.1 线写出器（wire realism）：把请求按「真实 Codex CLI（reqwest/
// hyper + OpenSSL）」的线格式逐字节写出——全小写头、固定插入序、显式
// content-length、无 accept-encoding（codex 的 reqwest 未启用 gzip feature）。
// Go 的 net/http 会把头规范化成大写驼峰并按字母序发送，还会透明补
// Accept-Encoding: gzip，JA4H 一眼可辨。TLS 层复用现有 utls 指纹拨号器，
// 因此换上本写出器后从 ClientHello 到 HTTP 头与真实客户端全层面同构。
//
// 与打票探测的 openAITicketWireRoundTripper（单连接一次性）不同，本实现是
// 完整的 http.RoundTripper：带 keep-alive 连接池（连接在响应体读尽并 Close 后
// 回池复用）、ResponseHeaderTimeout、ctx 取消中断，可直接挂在 http.Client
// 的 Transport 上替换 net/http.Transport。

// CodexH1HeaderOrder 是 codex-rs 0.157.1 ChatGPT 模式 HTTP 请求头的真实
// 写出顺序（构造序 = 插入序）。依据 http crate HeaderMap 的迭代语义逐字段
// 从 build_session_headers/auth 源码提取：
//
//	originator → user-agent → version → x-codex-beta-features →
//	x-codex-turn-state → x-codex-window-id → x-codex-turn-metadata →
//	x-codex-parent-thread-id → x-openai-subagent → x-oai-attestation →
//	x-openai-internal-codex-responses-lite → x-client-request-id →
//	session-id → thread-id → accept → authorization → chatgpt-account-id →
//	x-openai-fedramp → content-type（host 始终首位，content-length 始终末位）
//
// 顺序表只决定「存在的头」的相对位置；缺失的头自然跳过。
var CodexH1HeaderOrder = []string{
	"originator",
	"user-agent",
	"version",
	"x-codex-beta-features",
	"x-codex-turn-state",
	"x-codex-window-id",
	"x-codex-turn-metadata",
	"x-codex-parent-thread-id",
	"x-openai-subagent",
	"x-oai-attestation",
	"x-openai-internal-codex-responses-lite",
	"x-client-request-id",
	"session-id",
	"thread-id",
	"accept",
	"authorization",
	"chatgpt-account-id",
	"x-openai-fedramp",
	"content-type",
}

// WireH1Config 手工 H1 写出器的可调参数。
type WireH1Config struct {
	// HeaderOrder 固定头序（全小写）；nil 表示无固定序（全部按字母序）。
	HeaderOrder []string
	// MaxIdleConnsPerHost 每目标地址空闲连接上限（0 = 4）。
	MaxIdleConnsPerHost int
	// IdleConnTimeout 空闲连接超时（0 = 60s，较 net/http 默认 90s 保守：
	// CF/chatgpt.com 的服务端 keep-alive 可能短于客户端）。
	IdleConnTimeout time.Duration
	// ResponseHeaderTimeout 等待响应头超时（0 = 不限制；由上层 ctx 兜底）。
	ResponseHeaderTimeout time.Duration
	// ProbeTimeout 取用池内连接时的活性探测窗口（0 = 30ms）。探测以
	// SetReadDeadline+Read(1) 实现：超时=连接存活；EOF/错误/收到数据=死连
	// 丢弃重拨，避免死连接把请求打成传输错误。
	ProbeTimeout time.Duration
}

func (c *WireH1Config) normalized() WireH1Config {
	cfg := *c
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = 4
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 60 * time.Second
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 30 * time.Millisecond
	}
	return cfg
}

// wireH1IdleConn 池中的一条空闲连接（连接 + 其残留的 bufio 缓冲必须成对
// 复用：bufio 里可能缓冲着响应体末尾之后的字节）。
type wireH1IdleConn struct {
	conn   net.Conn
	br     *bufio.Reader
	idleAt time.Time
}

// WireH1Transport 手工 HTTP/1.1 keep-alive RoundTripper。并发安全；活跃连接
// 数不设上限（账号并发已在调度层受限），仅空闲侧有池上限。
type WireH1Transport struct {
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	cfg  WireH1Config

	mu   sync.Mutex
	idle map[string][]*wireH1IdleConn
}

// NewWireH1Transport 创建手工 H1 写出器。dial 必须返回已完成 TLS 握手的
// 连接（直接复用包内各指纹拨号器的 DialTLSContext）。
func NewWireH1Transport(dial func(ctx context.Context, network, addr string) (net.Conn, error), cfg WireH1Config) *WireH1Transport {
	return &WireH1Transport{
		dial: dial,
		cfg:  cfg.normalized(),
		idle: make(map[string][]*wireH1IdleConn),
	}
}

// RoundTrip 实现 http.RoundTripper。请求体会被完整缓冲（转发链的请求体
// 均为有限 JSON，上游原本也要求 Content-Length）。
func (t *WireH1Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("wire h1: nil URL")
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("wire h1: unsupported scheme %q", req.URL.Scheme)
	}
	if req.Body != nil {
		// http.Client 契约：RoundTrip 必须关闭请求体。
		defer func() { _ = req.Body.Close() }()
	}

	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("wire h1: read request body: %w", err)
		}
	}

	addr := req.URL.Host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}

	for {
		conn, br, pooled, err := t.acquire(req.Context(), addr)
		if err != nil {
			return nil, err
		}
		resp, err := t.roundTripOn(req, conn, br, body)
		if err == nil {
			return resp, nil
		}
		if !pooled {
			// 新拨连接上的失败不复用也不重试（上层 failover 处理）。
			_ = conn.Close()
			return nil, err
		}
		// 池内连接在写出/读头阶段失败（服务端恰好关闭等）：静默丢弃，
		// 换新连接重试一次——等价于 net/http 对可重试请求的处理。
		_ = conn.Close()
	}
}

// roundTripOn 在一条连接上完成一次请求-响应交换。
func (t *WireH1Transport) roundTripOn(req *http.Request, conn net.Conn, br *bufio.Reader, body []byte) (*http.Response, error) {
	// 写阶段：沿用 ctx deadline（若有）作为写超时。
	if deadline, ok := req.Context().Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	if err := writeWireH1Request(conn, req, body, t.cfg.HeaderOrder); err != nil {
		return nil, fmt.Errorf("wire h1: write request: %w", err)
	}

	// 读头阶段：ResponseHeaderTimeout 与 ctx deadline 取更早者；
	// ctx 取消时由 watcher 打断阻塞中的 Read。
	readDeadline := time.Time{}
	if t.cfg.ResponseHeaderTimeout > 0 {
		readDeadline = time.Now().Add(t.cfg.ResponseHeaderTimeout)
	}
	if deadline, ok := req.Context().Deadline(); ok && (readDeadline.IsZero() || deadline.Before(readDeadline)) {
		readDeadline = deadline
	}
	if !readDeadline.IsZero() {
		_ = conn.SetReadDeadline(readDeadline)
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}
	stopWatch := t.watchContext(req.Context(), conn)
	var resp *http.Response
	var err error
	for {
		resp, err = http.ReadResponse(br, req)
		if err != nil {
			stopWatch()
			return nil, fmt.Errorf("wire h1: read response: %w", err)
		}
		// 1xx 临时响应跳过（net/http Transport 同款语义）。
		if resp.StatusCode >= 100 && resp.StatusCode < 200 && resp.StatusCode != http.StatusSwitchingProtocols {
			_ = resp.Body.Close()
			continue
		}
		break
	}
	stopWatch()
	// 响应头已到，清零读 deadline：响应体（SSE 流）无整体超时，
	// ctx 取消由上层的 cancelOnCloseBody → body.Close → conn.Close 打断。
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	addr := wireH1Addr(req)
	reqWantsClose := req.Close || req.Header.Get("Connection") == "close"
	respWantsClose := resp.Close
	resp.Body = &wireH1Body{
		ReadCloser: resp.Body,
		t:          t,
		addr:       addr,
		conn:       conn,
		br:         br,
		noReuse:    reqWantsClose || respWantsClose,
	}
	return resp, nil
}

func wireH1Addr(req *http.Request) string {
	addr := req.URL.Host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}
	return addr
}

// acquire 取一条可用连接：优先池内（活性探测），否则新拨。pooled 表示
// 返回的连接是否来自池（用于失败重试判定）。
func (t *WireH1Transport) acquire(ctx context.Context, addr string) (net.Conn, *bufio.Reader, bool, error) {
	for {
		conn, br, ok := t.popIdle(addr)
		if !ok {
			break
		}
		if t.probeAlive(conn) {
			return conn, br, true, nil
		}
		_ = conn.Close()
	}
	conn, err := t.dial(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, false, fmt.Errorf("wire h1: dial %s: %w", addr, err)
	}
	return conn, bufio.NewReader(conn), false, nil
}

// popIdle 弹出一条未过期的空闲连接（LIFO，最热的连接优先）。
func (t *WireH1Transport) popIdle(addr string) (net.Conn, *bufio.Reader, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	stack := t.idle[addr]
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if time.Since(c.idleAt) > t.cfg.IdleConnTimeout {
			_ = c.conn.Close()
			continue
		}
		if len(stack) == 0 {
			delete(t.idle, addr)
		} else {
			t.idle[addr] = stack
		}
		return c.conn, c.br, true
	}
	delete(t.idle, addr)
	return nil, nil, false
}

// probeAlive 探测池内连接是否存活：HTTP/1.1 服务端不会主动发数据，
// 短窗 Read 超时 = 存活；EOF/错误/收到数据 = 死连（服务端已关或串流）。
func (t *WireH1Transport) probeAlive(conn net.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(t.cfg.ProbeTimeout))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	// 读超时 = 存活；EOF/复位 = 死连；读到数据 = 异常串流（对无流水线的
	// H1 服务器不可能，视为死连）。
	return isWireH1Timeout(err)
}

func isWireH1Timeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// watchContext 在 ctx 结束时把连接 deadline 置为过去，打断阻塞中的
// Read/Write（手工客户端没有 net/http 的自动取消管道）。返回的 stop 必须
// 在读头阶段结束后调用，避免 body 阶段误伤。
func (t *WireH1Transport) watchContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			_ = conn.SetDeadline(time.Time{})
		})
	}
}

// putIdle 把读尽的连接放回池；池满时淘汰最旧的一条。
// transport 永不真正「关闭」（缓存条目进程级存活），与 net/http Transport
// 的 CloseIdleConnections 语义一致：只清当前空闲连接，后续仍可继续池化。
func (t *WireH1Transport) putIdle(addr string, conn net.Conn, br *bufio.Reader) {
	c := &wireH1IdleConn{conn: conn, br: br, idleAt: time.Now()}
	t.mu.Lock()
	defer t.mu.Unlock()
	stack := append(t.idle[addr], c)
	if len(stack) > t.cfg.MaxIdleConnsPerHost {
		// 淘汰最旧的一条（栈底）。
		oldest := stack[0]
		stack = stack[1:]
		_ = oldest.conn.Close()
	}
	t.idle[addr] = stack
}

// CloseIdleConnections 实现 http.Client 的 closeIdler 契约：关闭全部空闲
// 连接（活跃请求不受影响；其连接在 body 关闭时照常回池）。
func (t *WireH1Transport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for addr, stack := range t.idle {
		for _, c := range stack {
			_ = c.conn.Close()
		}
		delete(t.idle, addr)
	}
}

// wireH1Body 包装响应体：跟踪是否读尽（EOF），Close 时把读尽的连接放回
// 池复用，未读尽的关闭（keep-alive 语义要求请求-响应完整成对）。
type wireH1Body struct {
	io.ReadCloser
	t       *WireH1Transport
	addr    string
	conn    net.Conn
	br      *bufio.Reader
	noReuse bool
	sawEOF  bool
	once    sync.Once
}

func (b *wireH1Body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.sawEOF = true
	}
	return n, err
}

func (b *wireH1Body) Close() error {
	var err error
	b.once.Do(func() {
		err = b.ReadCloser.Close()
		if b.sawEOF && !b.noReuse {
			b.t.putIdle(b.addr, b.conn, b.br)
		} else {
			_ = b.conn.Close()
		}
	})
	return err
}

// writeWireH1Request 手工序列化 HTTP/1.1 请求：
//   - host 始终首位；
//   - 顺序表内的头按表序小写输出（缺失即跳过）；
//   - 顺序表外的头按字母序补尾（语义不丢，形态尽量贴近）；
//   - 有体时显式 content-length 末位；
//   - 请求要求关闭时补 connection: close；
//   - 不写 accept-encoding（真实 codex 的 reqwest 未启用 gzip feature，
//     而 net/http 会透明补 Accept-Encoding: gzip——这是两者的可观测差异）。
func writeWireH1Request(w io.Writer, req *http.Request, body []byte, order []string) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", req.Method, req.URL.RequestURI())
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	b.WriteString("host: ")
	b.WriteString(host)

	written := make(map[string]bool, len(req.Header)+len(order)+2)
	written["host"] = true
	for _, name := range order {
		values, ok := req.Header[http.CanonicalHeaderKey(name)]
		if !ok {
			// 直接以小写键写入的头（不经 Set/Add）不会被规范化，补查一次。
			values, ok = req.Header[name]
		}
		if !ok || len(values) == 0 {
			continue
		}
		for _, v := range values {
			b.WriteString("\r\n")
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(v)
		}
		written[name] = true
	}
	if req.Close {
		b.WriteString("\r\nconnection: close")
		written["connection"] = true
	}
	// 顺序表外的头按字母序补尾；逐跳头与由本写出器管理的头不透传。
	extra := make([]string, 0, len(req.Header))
	extraValues := make(map[string][]string, len(req.Header))
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if written[lower] || wireH1SkipHeader(lower) {
			continue
		}
		extra = append(extra, lower)
		extraValues[lower] = values
	}
	sort.Strings(extra)
	for _, name := range extra {
		for _, v := range extraValues[name] {
			b.WriteString("\r\n")
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(v)
		}
	}
	if body != nil {
		b.WriteString("\r\ncontent-length: ")
		b.WriteString(strconv.Itoa(len(body)))
	}
	b.WriteString("\r\n\r\n")
	if body != nil {
		b.Write(body)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// wireH1SkipHeader 头是否由写出器管理或属逐跳头，不应从 req.Header 透传。
func wireH1SkipHeader(lower string) bool {
	switch lower {
	case "content-length", "transfer-encoding", "connection", "keep-alive",
		"proxy-connection", "upgrade", "te", "trailer", "host", "accept-encoding":
		return true
	}
	return false
}
