package infra

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/compforge/loopd/runtime/model"
)

func TransportError(err error) error {
	var network net.Error
	retryable := !errors.Is(err, context.Canceled) &&
		(errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
	return &model.Error{Message: err.Error(), Cause: err, Retryable: retryable}
}

func DecodeResponseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	err := &model.Error{StatusCode: response.StatusCode, Message: response.Status,
		Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	var envelope errorResponse
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope) == nil && envelope.Error.Message != "" {
		err.Type, err.Message = envelope.Error.Type, envelope.Error.Message
	}
	return err
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
