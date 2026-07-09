package dropclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func WithProxyURL(rawURL string) Option {
	return func(opts *Options) error {
		if strings.TrimSpace(rawURL) == "" {
			return nil
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("parse proxy URL: %w", err)
		}
		transport := defaultTransport()
		switch parsed.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks5", "socks5h":
			transport.Proxy = http.ProxyURL(parsed)
		default:
			return fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
		}
		opts.HTTPClient = &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		}
		return nil
	}
}

func WithSOCKS5Proxy(address string) Option {
	return WithProxyURL("socks5://" + address)
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
