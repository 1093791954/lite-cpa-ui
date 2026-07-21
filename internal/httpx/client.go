package httpx

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Shared high-performance transport for upstream calls.
var baseTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          512,
	MaxIdleConnsPerHost:   64,
	MaxConnsPerHost:       0,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ResponseHeaderTimeout: 0, // no timeout after headers (streaming)
}

var (
	clientCache sync.Map // proxyURL -> *http.Client
)

// Client returns a pooled HTTP client. Empty proxy uses shared transport.
func Client(proxyURL string) *http.Client {
	if proxyURL == "" {
		v, _ := clientCache.LoadOrStore("", &http.Client{Transport: baseTransport})
		return v.(*http.Client)
	}
	if v, ok := clientCache.Load(proxyURL); ok {
		return v.(*http.Client)
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		v, _ := clientCache.LoadOrStore("", &http.Client{Transport: baseTransport})
		return v.(*http.Client)
	}
	tr := baseTransport.Clone()
	tr.Proxy = http.ProxyURL(u)
	c := &http.Client{Transport: tr}
	actual, _ := clientCache.LoadOrStore(proxyURL, c)
	return actual.(*http.Client)
}

// Do is a thin helper that guarantees context cancellation closes the body path.
func Do(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = Client("")
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	return client.Do(req)
}
