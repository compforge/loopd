package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
)

const taskIDHeader = "X-Loopd-Task-ID"

type Chat struct {
	client *client
	state  *chatState
}

type chatState struct {
	mu        sync.Mutex
	sequences map[string]*messageSequence
	responses map[string]string
}

type messageSequence struct {
	mu   sync.Mutex
	next uint64
}

func newChat(client *client) Chat {
	return Chat{client: client, state: &chatState{sequences: make(map[string]*messageSequence), responses: make(map[string]string)}}
}

type CreateConversationRequest struct {
	Name string `json:"name"`
	// TaskID creates a work conversation owned by the Task's target. Without
	// it, the server creates a user conversation using the caller's identity.
	TaskID string `json:"task_id,omitempty"`
}

type SendMessageRequest struct {
	TaskID  string          `json:"task_id,omitempty"`
	UserKey string          `json:"user_key,omitempty"`
	Target  loopd.ActorRef  `json:"target,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type TaskFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (chat Chat) CreateConversation(
	ctx context.Context,
	request CreateConversationRequest,
) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := chat.client.do(ctx, http.MethodPost, "/v1/conversations", request, &result)
	return result, err
}

func (chat Chat) Conversation(ctx context.Context, conversationID string) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := chat.client.do(
		ctx,
		http.MethodGet,
		"/v1/conversations/"+url.PathEscape(conversationID),
		nil,
		&result,
	)
	return result, err
}

// Send is a Verb with a write effect when creating work without TaskID, or a
// read effect when observing existing work with TaskID. The HTTP connection only observes execution; closing the
// stream does not cancel the Task or its Operator.
func (chat Chat) Send(
	ctx context.Context,
	conversationID string,
	request SendMessageRequest,
	lastEventID string,
) (*ChatStream, error) {
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages"
	headers := map[string]string{"Accept": "text/event-stream"}
	if lastEventID != "" {
		headers["Last-Event-ID"] = lastEventID
	}
	response, err := chat.client.openWithHeaders(ctx, http.MethodPost, path, request, headers)
	if err != nil {
		return nil, err
	}
	if err := decodeResponseError(response); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		_ = response.Body.Close()
		return nil, fmt.Errorf("loop-server returned %q instead of text/event-stream", response.Header.Get("Content-Type"))
	}
	taskID := response.Header.Get(taskIDHeader)
	if taskID == "" {
		_ = response.Body.Close()
		return nil, errors.New("loop-server response omitted task ID")
	}
	stream := &ChatStream{
		taskID:      taskID,
		body:        response.Body,
		scanner:     bufio.NewScanner(response.Body),
		lastEventID: lastEventID,
	}
	stream.scanner.Buffer(make([]byte, 64<<10), 16<<20)
	return stream, nil
}

// ChatStream reads one AgentUE event at a time from the Chat SSE response.
type ChatStream struct {
	taskID      string
	body        io.ReadCloser
	scanner     *bufio.Scanner
	lastEventID string
}

func (stream *ChatStream) TaskID() string { return stream.taskID }

func (stream *ChatStream) Next() (loopd.Event, error) {
	eventID := stream.lastEventID
	var data bytes.Buffer
	for stream.scanner.Scan() {
		line := stream.scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			value := bytes.TrimSuffix(data.Bytes(), []byte{'\n'})
			stream.lastEventID = eventID
			var envelope struct {
				MessageID string          `json:"message_id"`
				Message   *loopd.Message  `json:"message"`
				Event     json.RawMessage `json:"event"`
			}
			if err := json.Unmarshal(value, &envelope); err != nil {
				return loopd.Event{}, err
			}
			if envelope.MessageID != "" {
				return loopd.Event{ID: eventID, Data: envelope.Event, MessageID: envelope.MessageID, Message: envelope.Message}, nil
			}
			return loopd.Event{ID: eventID, Data: append(json.RawMessage(nil), value...)}, nil
		}
		if strings.HasPrefix(line, "id:") {
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := stream.scanner.Err(); err != nil {
		return loopd.Event{}, err
	}
	return loopd.Event{}, io.EOF
}

func (stream *ChatStream) Close() error { return stream.body.Close() }

func (chat Chat) History(
	ctx context.Context,
	conversationID string,
	after string,
	limit int,
) ([]loopd.Message, error) {
	var result page[loopd.Message]
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages?after=" + url.QueryEscape(after) +
		"&limit=" + strconv.Itoa(limit)
	err := chat.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

// Emit is a Verb (effect: write) publishing to the initial main answer. Other messages
// use EmitMessage; each message owns its AgentUE sequence.
func (chat Chat) Emit(ctx context.Context, taskID string, event agentueui.Event) (loopd.Event, error) {
	chat.state.mu.Lock()
	messageID := chat.state.responses[taskID]
	chat.state.mu.Unlock()
	if messageID == "" {
		task, err := (Task{client: chat.client}).Get(ctx, taskID)
		if err != nil {
			return loopd.Event{}, err
		}
		messageID = task.Response.ID
		if messageID == "" {
			return loopd.Event{}, fmt.Errorf("task %q has no initial response", taskID)
		}
		chat.state.mu.Lock()
		chat.state.responses[taskID] = messageID
		chat.state.mu.Unlock()
	}
	return chat.EmitMessage(ctx, taskID, messageID, event)
}

// Output is a Verb (effect: write) creating or reusing a message by stable Task/key.
func (chat Chat) Output(ctx context.Context, taskID string, request loopd.OutputRequest) (loopd.Message, error) {
	var message loopd.Message
	err := chat.client.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/outputs", request, &message)
	return message, err
}

// EmitMessage is a Verb (effect: write). Equal block IDs and sequence numbers in other
// messages never collide. The server verifies the message belongs to this Task.
func (chat Chat) EmitMessage(ctx context.Context, taskID, messageID string, event agentueui.Event) (loopd.Event, error) {
	result, err := chat.emit(ctx, "message/"+messageID, "/v1/tasks/"+url.PathEscape(taskID)+"/messages/"+url.PathEscape(messageID)+"/events", event)
	result.MessageID = messageID
	return result, err
}

func (chat Chat) emit(ctx context.Context, sequenceKey, path string, event agentueui.Event) (loopd.Event, error) {
	sequence := chat.sequence(sequenceKey)
	sequence.mu.Lock()
	defer sequence.mu.Unlock()
	if event.Seq == 0 {
		event.Seq = sequence.next
	}
	data, err := event.Marshal()
	if err != nil {
		return loopd.Event{}, err
	}
	var result struct {
		ID string `json:"id"`
	}
	err = chat.client.do(ctx, http.MethodPost, path, struct {
		Event json.RawMessage `json:"event"`
	}{Event: data}, &result)
	if err != nil {
		return loopd.Event{}, err
	}
	if event.Seq >= sequence.next {
		sequence.next = event.Seq + 1
	}
	return loopd.Event{ID: result.ID, Data: data}, nil
}

func (chat Chat) sequence(messageID string) *messageSequence {
	chat.state.mu.Lock()
	defer chat.state.mu.Unlock()
	sequence := chat.state.sequences[messageID]
	if sequence == nil {
		// loop-server initializes every message stream with AgentUE seq 1.
		sequence = &messageSequence{next: 2}
		chat.state.sequences[messageID] = sequence
	}
	return sequence
}

// Complete is a Verb (effect: write) that persists all output Messages and
// closes the task delivery. Repeating the same completion is safe.
func (chat Chat) Complete(ctx context.Context, taskID string, failure *TaskFailure) error {
	return chat.client.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/complete", struct {
		Error *TaskFailure `json:"error,omitempty"`
	}{Error: failure}, nil)
}

// IsEnd reports Task completion on a Chat stream, not the end of a work message.
func IsEnd(event loopd.Event) (bool, error) {
	parsed, err := agentueui.Parse(event.Data)
	if err != nil {
		return false, err
	}
	return parsed.Op == agentueui.OpEnd && (event.Message == nil || event.Message.Purpose == "response"), nil
}
