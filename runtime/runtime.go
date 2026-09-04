// Package runtime provides the Go client embedded by Operator reconcilers.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Options struct {
	HTTPClient   *http.Client
	PollInterval time.Duration
}

type Runtime struct {
	Loop Loop
}

type Loop struct {
	Chat     Chat
	Harness  Harness
	Operator Operator
	client   *client
}

func New(baseURL string, options Options) (*Runtime, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid loop-server URL %q", baseURL)
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	c := &client{baseURL: parsed, http: options.HTTPClient, pollInterval: options.PollInterval}
	loop := Loop{client: c}
	loop.Chat = Chat{client: c}
	loop.Harness = Harness{client: c}
	loop.Operator = Operator{client: c}
	return &Runtime{Loop: loop}, nil
}

type client struct {
	baseURL      *url.URL
	http         *http.Client
	pollInterval time.Duration
}

func (client *client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL.String()+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope errorResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err == nil && envelope.Error.Message != "" {
			return &Error{StatusCode: response.StatusCode, Type: envelope.Error.Type, Message: envelope.Error.Message}
		}
		return &Error{StatusCode: response.StatusCode, Message: response.Status}
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode loop-server response: %w", err)
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
