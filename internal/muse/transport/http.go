package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type HTTP struct {
	Client  *http.Client
	BaseURL *url.URL
}

func NewHTTP(base string, timeout time.Duration, proxy string) (*HTTP, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid Muse base URL")
	}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	client := &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	return &HTTP{Client: client, BaseURL: u}, nil
}
func (h *HTTP) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	u := h.BaseURL.ResolveReference(rel)
	if u.Host != h.BaseURL.Host {
		return nil, errors.New("upstream path escapes configured host")
	}
	return http.NewRequestWithContext(ctx, method, u.String(), body)
}
func (h *HTTP) Close() {
	if tr, ok := h.Client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
