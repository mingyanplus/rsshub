package proxyutil

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// Transport 根据代理地址构造 http.Transport。
// 支持 http://、https://、socks5://、socks5h:// 协议；空串或无法解析时返回 nil（不走代理）。
func Transport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}

	switch u.Scheme {
	case "http", "https":
		return &http.Transport{
			Proxy: http.ProxyURL(u),
		}
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil
		}
		ctxDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil
		}
		return &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return ctxDialer.DialContext(ctx, network, addr)
			},
		}
	}
	return nil
}

// Apply 为客户端设置代理；proxyURL 为空时清除代理。
// timeout 保持调用方原有设置（timeout <= 0 时不修改）。
func Apply(client *http.Client, proxyURL string) {
	if client == nil {
		return
	}
	if t := Transport(proxyURL); t != nil {
		client.Transport = t
	} else {
		client.Transport = nil
	}
}

// NewClient 构造带可选代理与超时的客户端
func NewClient(proxyURL string, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	Apply(c, proxyURL)
	return c
}
