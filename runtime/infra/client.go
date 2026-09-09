package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/compforge/loopd/runtime/model"
)

type Options struct {
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	Logger         *slog.Logger
}

// NewClient creates bounded short-request transport; streaming calls use the caller's context.
func NewClient(baseURL string, options Options) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, &model.Error{Message: fmt.Sprintf("invalid loop-server URL %q", baseURL)}
	}
	if options.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = 5 * time.Second
		transport.ResponseHeaderTimeout = 30 * time.Second
		transport.IdleConnTimeout = 90 * time.Second
		transport.MaxIdleConns = 100
		transport.MaxIdleConnsPerHost = 20
		jar, _ := cookiejar.New(nil)
		options.HTTPClient = &http.Client{Transport: transport, Jar: jar}
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = 30 * time.Second
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Client{
		baseURL: parsed, http: options.HTTPClient, logger: options.Logger,
		requestTimeout: options.RequestTimeout,
	}, nil
}

type Client struct {
	logger         *slog.Logger
	baseURL        *url.URL
	http           *http.Client
	requestTimeout time.Duration
}

func (client *Client) Do(ctx context.Context, method, path string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()

	response, err := client.Open(ctx, method, path, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := DecodeResponseError(response); err != nil {
		return err
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	// Successful logical content can span many bounded storage parts. Keep the
	// request deadline, but do not truncate a result already accepted by server.
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return TransportError(fmt.Errorf("decode loop-server response: %w", err))
	}
	return nil
}

func (client *Client) Open(ctx context.Context, method, path string, input any) (*http.Response, error) {
	return client.openWithHeaders(ctx, method, path, input, nil)
}

func (client *Client) openWithHeaders(
	ctx context.Context,
	method string,
	path string,
	input any,
	headers map[string]string,
) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, model.WrapError(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL.String()+path, body)
	if err != nil {
		return nil, model.WrapError(err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, TransportError(err)
	}
	return response, nil
}

func (c *Client) Logger() *slog.Logger          { return c.logger }
func (c *Client) RequestTimeout() time.Duration { return c.requestTimeout }
