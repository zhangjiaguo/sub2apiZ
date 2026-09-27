package repository

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// codexWire 标记生成独立缓存条目，transport 是手工 H1 写出器；warm 与
// wire 同时置位时 wire 让位于 warm（打票出口覆盖的每请求新出口语义）。
func (s *HTTPUpstreamSuite) TestCodexWireSeparateCacheEntry() {
	svc := s.newService()
	plain, err := svc.getClientEntryWithTLS("", 1, 1, &tlsfingerprint.Profile{Name: "wire-test"}, service.HTTPUpstreamProfileDefault, false, false, false, false)
	require.NoError(s.T(), err)
	wire, err := svc.getClientEntryWithTLS("", 1, 1, &tlsfingerprint.Profile{Name: "wire-test"}, service.HTTPUpstreamProfileDefault, false, false, false, true)
	require.NoError(s.T(), err)
	require.NotSame(s.T(), plain, wire, "wire 与非 wire 应是不同缓存条目")

	_, isWire := wire.client.Transport.(*tlsfingerprint.WireH1Transport)
	require.True(s.T(), isWire, "wire 条目应挂 WireH1Transport, got %T", wire.client.Transport)
	_, isPlainTransport := plain.client.Transport.(*http.Transport)
	require.True(s.T(), isPlainTransport, "普通条目应挂 net/http Transport")

	// 再次获取命中各自缓存。
	wireAgain, err := svc.getClientEntryWithTLS("", 1, 1, &tlsfingerprint.Profile{Name: "wire-test"}, service.HTTPUpstreamProfileDefault, false, false, false, true)
	require.NoError(s.T(), err)
	require.Same(s.T(), wire, wireAgain)

	// warm 优先于 wire。
	warmWins, err := svc.getClientEntryWithTLS("", 1, 1, &tlsfingerprint.Profile{Name: "wire-test"}, service.HTTPUpstreamProfileDefault, false, false, true, true)
	require.NoError(s.T(), err)
	_, warmIsWire := warmWins.client.Transport.(*tlsfingerprint.WireH1Transport)
	require.False(s.T(), warmIsWire, "warm+wire 同时置位时 wire 应让位")
}

// wire transport 构建：直连/socks5/http 代理分别产出可用的 wire transport；
// https 代理与未知 scheme 回落普通指纹 transport。
func TestBuildWireH1Transport(t *testing.T) {
	// 直连。
	rt, err := buildWireH1Transport(poolSettings{}, nil, &tlsfingerprint.Profile{Name: "t"})
	require.NoError(t, err)
	_, ok := rt.(*tlsfingerprint.WireH1Transport)
	require.True(t, ok, "direct should build wire transport, got %T", rt)

	// socks5。
	proxyURL, err := url.Parse("socks5://user:pass@socks.example:1080")
	require.NoError(t, err)
	rt, err = buildWireH1Transport(poolSettings{}, proxyURL, &tlsfingerprint.Profile{Name: "t"})
	require.NoError(t, err)
	_, ok = rt.(*tlsfingerprint.WireH1Transport)
	require.True(t, ok, "socks5 should build wire transport")

	// http 代理。
	proxyURL, err = url.Parse("http://user:pass@http-proxy.example:10000")
	require.NoError(t, err)
	rt, err = buildWireH1Transport(poolSettings{}, proxyURL, &tlsfingerprint.Profile{Name: "t"})
	require.NoError(t, err)
	_, ok = rt.(*tlsfingerprint.WireH1Transport)
	require.True(t, ok, "http proxy should build wire transport")

	// https 代理：回落普通 transport（指纹拨号器无法与 https 代理握手）。
	proxyURL, err = url.Parse("https://proxy.example:443")
	require.NoError(t, err)
	rt, err = buildWireH1Transport(poolSettings{}, proxyURL, &tlsfingerprint.Profile{Name: "t"})
	require.NoError(t, err)
	_, ok = rt.(*tlsfingerprint.WireH1Transport)
	require.False(t, ok, "https proxy must fall back to plain transport")
	_, ok = rt.(*http.Transport)
	require.True(t, ok, "https proxy fallback should be *http.Transport")
}
