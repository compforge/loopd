// Package runtime provides the Go collaboration toolkit embedded by Operator reconcilers.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/compforge/loopd/harness"
)

type Options struct {
	HTTPClient            *http.Client
	RequestTimeout        time.Duration
	RegistryLeaseDuration time.Duration
	Harnesses             map[string]harness.Adapter
	Logger                *slog.Logger
}

type Runtime struct {
	Loop   Loop
	cancel context.CancelFunc
}

// Loop exposes collaboration Verbs. A Verb's effect is read (observe existing
// facts) or write (initiate work or change collaboration state). Identity and
// retry guarantees belong to each Verb, not to a generic persistent Effect engine.
type Loop struct {
	Conv     Conv
	Human    Human
	Harness  Harness
	Operator Operator
}

func New(baseURL string, options Options) (*Runtime, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid loop-server URL %q", baseURL)
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
	c := &client{
		baseURL: parsed, http: options.HTTPClient, logger: options.Logger,
		requestTimeout: options.RequestTimeout,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	loop := Loop{}
	loop.Conv = Conv{client: c, messages: &messageHandles{values: make(map[string]*Message)}}
	loop.Human = Human{client: c}
	loop.Harness = newHarness(runCtx, c, options.RegistryLeaseDuration, options.Harnesses, options.Logger)
	loop.Harness.conv = loop.Conv
	loop.Operator = Operator{registry: newRegistry(
		runCtx, c, "operator", "operators", options.RegistryLeaseDuration, options.Logger,
	)}
	return &Runtime{Loop: loop, cancel: cancel}, nil
}

// Close stops process-local Harness executions. A durable Adapter remains
// resumable through its Harness service; the demo agentgo Adapter does not.
func (runtime *Runtime) Close() error {
	if runtime.cancel != nil {
		runtime.cancel()
	}
	return nil
}

type client struct {
	logger         *slog.Logger
	baseURL        *url.URL
	http           *http.Client
	requestTimeout time.Duration
}

func (client *client) do(ctx context.Context, method, path string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()

	response, err := client.open(ctx, method, path, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := decodeResponseError(response); err != nil {
		return err
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode loop-server response: %w", err)
	}
	return nil
}

func (client *client) open(ctx context.Context, method, path string, input any) (*http.Response, error) {
	return client.openWithHeaders(ctx, method, path, input, nil)
}

func (client *client) openWithHeaders(
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
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL.String()+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func decodeResponseError(response *http.Response) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope errorResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err == nil && envelope.Error.Message != "" {
			return &Error{StatusCode: response.StatusCode, Type: envelope.Error.Type, Message: envelope.Error.Message}
		}
		return &Error{StatusCode: response.StatusCode, Message: response.Status}
	}
	return nil
}

type Error struct {
	StatusCode int
	Type       string
	Message    string
}

func (err *Error) Error() string {
	if err.Type == "" {
		return err.Message
	}
	return err.Type + ": " + err.Message
}

func IsConflict(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.StatusCode == http.StatusConflict
}
