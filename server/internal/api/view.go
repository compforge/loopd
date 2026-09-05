package api

import (
	"encoding/json"

	loopd "github.com/compforge/loopd"
)

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type page[T any] struct {
	Data []T `json:"data"`
}

type createConversationRequest struct {
	Name   string `json:"name,omitempty"`
	TaskID string `json:"task_id,omitempty"`
}

type registrationRequest struct {
	DisplayName  string `json:"display_name,omitempty"`
	Description  string `json:"description,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type createChatMessagesRequest struct {
	TaskID  string          `json:"task_id,omitempty"`
	UserKey string          `json:"user_key,omitempty"`
	Target  loopd.ActorRef  `json:"target,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type appendTaskEventRequest struct {
	Event json.RawMessage `json:"event"`
}

type appendTaskEventResponse struct {
	ID string `json:"id"`
}

type completeTaskRequest struct {
	Error *taskFailure `json:"error,omitempty"`
}

type taskFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
