// Package httpclient provides isolated request and stream connection pools for
// HTTP Harness adapters. Protocol handling and recovery stay with the adapter.
package httpclient

import (
	"errors"
	"net"
	"net/http"
	"time"
)

type Config struct {
	RequestTimeout time.Duration
	MaxConnections int // Per host, independently for each pool.
}

// Client is shared by all calls of an adapter and is safe for concurrent use.
type Client struct {
	short  *http.Client
	stream *http.Client
}

// New requires a positive timeout and connection limit. Adapter configuration
// supplies defaults so its effective limits remain explicit.
func New(config Config) (*Client, error) {
	if config.RequestTimeout <= 0 || config.MaxConnections <= 0 {
		return nil, errors.New("HTTP request timeout and connection limit must be positive")
	}
	transport := func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DialContext = (&net.Dialer{Timeout: config.RequestTimeout, KeepAlive: 30 * time.Second}).DialContext
		t.TLSHandshakeTimeout = config.RequestTimeout
		t.ResponseHeaderTimeout = config.RequestTimeout
		t.MaxConnsPerHost = config.MaxConnections
		t.MaxIdleConns = config.MaxConnections
		t.MaxIdleConnsPerHost = config.MaxConnections
		t.IdleConnTimeout = time.Minute
		return t
	}
	return &Client{
		short:  &http.Client{Transport: transport(), Timeout: config.RequestTimeout},
		stream: &http.Client{Transport: transport()},
	}, nil
}

// DoShort bounds the entire request, including reading the response body.
// The caller must close the response body.
func (c *Client) DoShort(req *http.Request) (*http.Response, error) {
	return c.short.Do(req)
}

// DoStream bounds connection setup and response headers, but leaves the stream
// lifetime to req.Context(). The caller must close the response body.
func (c *Client) DoStream(req *http.Request) (*http.Response, error) {
	return c.stream.Do(req)
}

// CloseIdleConnections releases idle connections in both pools without
// interrupting active requests or streams.
func (c *Client) CloseIdleConnections() {
	c.short.CloseIdleConnections()
	c.stream.CloseIdleConnections()
}

// DoFunc adapts a request method to the HTTP doer interface accepted by SDKs.
type DoFunc func(*http.Request) (*http.Response, error)

func (f DoFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }
