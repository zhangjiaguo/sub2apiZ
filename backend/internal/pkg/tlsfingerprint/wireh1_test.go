package tlsfingerprint

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// —— 测试基建：自签 TLS 服务器，逐字节记录进来的请求，按脚本回响应 ——

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "wire-h1-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// wireH1TestServer 极简 TLS HTTP/1.1 服务器：记录原始请求字节与连接数，
// 每个响应由 respond 决定（返回要写的字节；返回 nil 表示直接关连接）。
type wireH1TestServer struct {
	t        *testing.T
	ln       net.Listener
	mu       sync.Mutex
	requests []string // 每个请求的原始字节（不含 body 也可含）
	conns    int      // 已接受的连接数
	respond  func(rawReq string, conn net.Conn, nReqOnConn int) []byte
}

func newWireH1TestServer(t *testing.T, respond func(raw string, conn net.Conn, n int) []byte) *wireH1TestServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cert := selfSignedCert(t)
	srv := &wireH1TestServer{t: t, ln: ln, respond: respond}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			srv.mu.Lock()
			srv.conns++
			srv.mu.Unlock()
			conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
			go srv.serveConn(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return srv
}

func (s *wireH1TestServer) addr() string { return s.ln.Addr().String() }

func (s *wireH1TestServer) record(raw string) {
	s.mu.Lock()
	s.requests = append(s.requests, raw)
	s.mu.Unlock()
}

func (s *wireH1TestServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *wireH1TestServer) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *wireH1TestServer) lastRequest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return ""
	}
	return s.requests[len(s.requests)-1]
}

// serveConn 在一条连接上循环读请求并回响应（keep-alive）。
func (s *wireH1TestServer) serveConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	br := bufio.NewReader(conn)
	nReq := 0
	for {
		raw, err := readWireH1TestRequest(br)
		if err != nil {
			return
		}
		nReq++
		s.record(raw)
		resp := s.respond(raw, conn, nReq)
		if resp == nil {
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// readWireH1TestRequest 读一个完整请求（头 + content-length body）返回原始字节。
func readWireH1TestRequest(br *bufio.Reader) (string, error) {
	var raw strings.Builder
	contentLength := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		raw.WriteString(line)
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "content-length:") {
			n := 0
			for _, ch := range strings.TrimSpace(trimmed[len("content-length:"):]) {
				n = n*10 + int(ch-'0')
			}
			contentLength = n
		}
	}
	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(br, body); err != nil {
			return "", err
		}
		raw.Write(body)
	}
	return raw.String(), nil
}

const wireH1TestOKResponse = "HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ncontent-length: 5\r\n\r\nhello"

// newWireH1TestTransport 构建直连本测试服务器的 wire transport。
func newWireH1TestTransport(t *testing.T, addr string, cfg WireH1Config) *WireH1Transport {
	t.Helper()
	dial := func(ctx context.Context, network, a string) (net.Conn, error) {
		d := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, ServerName: "wire-h1-test"}}
		return d.DialContext(ctx, network, a)
	}
	return NewWireH1Transport(dial, cfg)
}

func newWireH1TestRequest(t *testing.T, addr, method, path, body string) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "https://"+addr+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

// —— 用例 ——

// 线格式核心断言：小写、固定插入序、host 首位、content-length 末位、
// 顺序表外头字母序补尾、无 accept-encoding。
func TestWireH1RequestWireFormat(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{HeaderOrder: CodexH1HeaderOrder})

	req := newWireH1TestRequest(t, srv.addr(), "POST", "/backend-api/codex/responses", `{"model":"gpt-5"}`)
	req.Host = "chatgpt.com"
	// 打乱 Set 顺序：真实链路里头的插入序也不保证与 codex 序一致，写出器
	// 必须按顺序表归位。
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("authorization", "Bearer test-token")
	req.Header.Set("chatgpt-account-id", "acct-123")
	req.Header.Set("session-id", "sess-1")
	req.Header.Set("thread-id", "thread-1")
	req.Header.Set("x-client-request-id", "thread-1")
	req.Header.Set("user-agent", "codex_cli_rs/0.157.1")
	req.Header.Set("version", "0.157.1")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("x-codex-beta-features", "responses_2025")
	req.Header.Set("x-codex-window-id", "win-1")
	req.Header.Set("x-codex-turn-metadata", "{}")
	req.Header.Set("x-codex-turn-state", "ticket-blob")
	// 顺序表外的头：应按字母序补尾。
	req.Header.Set("x-zeta", "z")
	req.Header.Set("x-alpha", "a")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "hello" {
		t.Fatalf("body = %q", b)
	}

	raw := srv.lastRequest()
	lines := strings.SplitN(raw, "\r\n\r\n", 2)
	head := strings.Split(lines[0], "\r\n")
	wantPrefix := []string{
		"POST /backend-api/codex/responses HTTP/1.1",
		"host: chatgpt.com",
		"originator: codex_cli_rs",
		"user-agent: codex_cli_rs/0.157.1",
		"version: 0.157.1",
		"x-codex-beta-features: responses_2025",
		"x-codex-turn-state: ticket-blob",
		"x-codex-window-id: win-1",
		"x-codex-turn-metadata: {}",
		"x-client-request-id: thread-1",
		"session-id: sess-1",
		"thread-id: thread-1",
		"accept: text/event-stream",
		"authorization: Bearer test-token",
		"chatgpt-account-id: acct-123",
		"content-type: application/json",
		"x-alpha: a", // 顺序表外：字母序补尾，在 content-length 前
		"x-zeta: z",
		"content-length: " + strconv.Itoa(len(`{"model":"gpt-5"}`)),
	}
	if len(head) != len(wantPrefix) {
		t.Fatalf("header line count = %d, want %d; raw:\n%s", len(head), len(wantPrefix), raw)
	}
	for i, want := range wantPrefix {
		if head[i] != want {
			t.Fatalf("header[%d] = %q, want %q\nraw:\n%s", i, head[i], want, raw)
		}
	}
	if body := lines[1]; body != `{"model":"gpt-5"}` {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(strings.ToLower(raw), "accept-encoding") {
		t.Fatalf("accept-encoding must not be sent:\n%s", raw)
	}
}

// keep-alive：读尽并 Close 后连接回池复用（两次请求一条连接）。
func TestWireH1KeepAliveReuse(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	for i := 0; i < 3; i++ {
		req := newWireH1TestRequest(t, srv.addr(), "POST", "/x", `{"i":`+string(rune('0'+i))+`}`)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("round trip %d: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if got := srv.connCount(); got != 1 {
		t.Fatalf("conn count = %d, want 1 (keep-alive reuse)", got)
	}
}

// 未读尽的 body 关闭后连接关闭（不回池），下次请求新拨。
func TestWireH1UndrainedBodyClosesConn(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	_ = resp.Body.Close() // 不读尽
	resp2, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
	if err != nil {
		t.Fatalf("round trip 2: %v", err)
	}
	_, _ = io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if got := srv.connCount(); got != 2 {
		t.Fatalf("conn count = %d, want 2", got)
	}
}

// 请求带 Close（或 connection: close 头）：写出 connection: close 且不复用。
func TestWireH1RequestCloseHeader(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	for i := 0; i < 2; i++ {
		req := newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}")
		req.Close = true
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("round trip %d: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	raw := srv.lastRequest()
	if !strings.Contains(raw, "\r\nconnection: close\r\n") {
		t.Fatalf("connection: close missing:\n%s", raw)
	}
	if got := srv.connCount(); got != 2 {
		t.Fatalf("conn count = %d, want 2", got)
	}
}

// 服务端响应 Connection: close：连接不复用。
func TestWireH1ServerCloseHeaderNoReuse(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		if n == 1 {
			return []byte("HTTP/1.1 200 OK\r\ncontent-length: 5\r\nconnection: close\r\n\r\nhello")
		}
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	for i := 0; i < 2; i++ {
		resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
		if err != nil {
			t.Fatalf("round trip %d: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if got := srv.connCount(); got != 2 {
		t.Fatalf("conn count = %d, want 2", got)
	}
}

// chunked 响应解析 + 读尽回池。
func TestWireH1ChunkedResponse(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte("HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ntransfer-encoding: chunked\r\n\r\n5\r\nhello\r\n3\r\n by\r\n0\r\n\r\n")
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	for i := 0; i < 2; i++ {
		resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
		if err != nil {
			t.Fatalf("round trip %d: %v", i, err)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read chunked body: %v", err)
		}
		if string(b) != "hello by" {
			t.Fatalf("body = %q", b)
		}
		_ = resp.Body.Close()
	}
	if got := srv.connCount(); got != 1 {
		t.Fatalf("conn count = %d, want 1", got)
	}
}

// ResponseHeaderTimeout：服务端拖延响应头时按配置超时。
func TestWireH1ResponseHeaderTimeout(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		time.Sleep(500 * time.Millisecond)
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{ResponseHeaderTimeout: 80 * time.Millisecond})

	start := time.Now()
	_, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("timeout too slow: %v", elapsed)
	}
}

// ctx 取消：读头阻塞时取消 ctx 应立即打断。
func TestWireH1ContextCancelInterrupts(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		time.Sleep(2 * time.Second)
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	ctx, cancel := context.WithCancel(context.Background())
	req := newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}")
	req = req.WithContext(ctx)
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatalf("expected cancel error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancel too slow: %v", elapsed)
	}
}

// 空闲超时：过期连接不回用。
func TestWireH1IdleTimeoutDiscards(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{IdleConnTimeout: 40 * time.Millisecond})

	do := func() {
		resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	do()
	time.Sleep(120 * time.Millisecond)
	do()
	if got := srv.connCount(); got != 2 {
		t.Fatalf("conn count = %d, want 2 (expired idle conn discarded)", got)
	}
}

// 池内死连接：取用时活性探测发现并丢弃，换新连接重试成功（对上层零感知）。
func TestWireH1DeadPooledConnRedials(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		if n == 1 {
			// 回完第一个响应后立刻关连接（不带 connection: close，模拟
			// 服务端先于客户端发现的半死连接）。
			go func() {
				time.Sleep(20 * time.Millisecond)
				_ = conn.Close()
			}()
		}
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})

	do := func() {
		resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	do()
	time.Sleep(100 * time.Millisecond) // 让服务端把连接关掉
	do()
	if got := srv.requestCount(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

// 并发请求各自成功；MaxIdleConnsPerHost 限制回池数量。
func TestWireH1ConcurrentRequests(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte(wireH1TestOKResponse)
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{MaxIdleConnsPerHost: 2})

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := rt.RoundTrip(newWireH1TestRequest(t, srv.addr(), "POST", "/x", "{}"))
			if err != nil {
				t.Errorf("round trip: %v", err)
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	if got := srv.requestCount(); got != 6 {
		t.Fatalf("request count = %d, want 6", got)
	}
	rt.CloseIdleConnections()
}

// 非 https 请求直接报错（dial 侧只产出 TLS 连接）。
func TestWireH1RejectsPlainHTTP(t *testing.T) {
	rt := NewWireH1Transport(func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Fatalf("dial must not be called")
		return nil, nil
	}, WireH1Config{})
	req, _ := http.NewRequest("GET", "http://example.com/x", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatalf("expected error for http scheme")
	}
}

// 作为 http.Client 的 Transport 使用（RoundTripper 契约：请求体由
// RoundTrip 关闭、响应体由调用方关闭）。
func TestWireH1ViaHTTPClient(t *testing.T) {
	srv := newWireH1TestServer(t, func(raw string, conn net.Conn, n int) []byte {
		return []byte("HTTP/1.1 200 OK\r\ncontent-type: application/json\r\ncontent-length: 11\r\n\r\n{\"ok\":true}")
	})
	rt := newWireH1TestTransport(t, srv.addr(), WireH1Config{})
	client := &http.Client{Transport: rt}

	resp, err := client.Post("https://"+srv.addr()+"/v1/x", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("client post: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != `{"ok":true}` {
		t.Fatalf("body = %q", b)
	}
}
