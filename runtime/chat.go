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
	sequences map[string]*taskSequence
}

type taskSequence struct {
	mu   sync.Mutex
	next uint64
}

func newChat(client *client) Chat {
	return Chat{client: client, state: &chatState{sequences: make(map[string]*taskSequence)}}
}

type CreateConversationRequest struct {
	Name            string `json:"name"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
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

// Send starts a question when TaskID is empty, or resumes the same task when
// TaskID is present. The HTTP connection only observes execution; closing the
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

// Emit publishes one Operator-produced set or append event for a Task. A zero
// AgentUE seq is assigned from this Runtime's per-Task sequence.
func (chat Chat) Emit(ctx context.Context, taskID string, event agentueui.Event) (loopd.Event, error) {
	sequence := chat.sequence(taskID)
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
	err = chat.client.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/events", struct {
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

func (chat Chat) sequence(taskID string) *taskSequence {
	chat.state.mu.Lock()
	defer chat.state.mu.Unlock()
	sequence := chat.state.sequences[taskID]
	if sequence == nil {
		// loop-server initializes every Task stream with AgentUE seq 1.
		sequence = &taskSequence{next: 2}
		chat.state.sequences[taskID] = sequence
	}
	return sequence
}

// Complete persists the latest AgentUE snapshot as the response Message and
// closes the task delivery. Repeating the same completion is safe.
func (chat Chat) Complete(ctx context.Context, taskID string, failure *TaskFailure) error {
	return chat.client.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/complete", struct {
		Error *TaskFailure `json:"error,omitempty"`
	}{Error: failure}, nil)
}

func IsEnd(event loopd.Event) (bool, error) {
	parsed, err := agentueui.Parse(event.Data)
	if err != nil {
		return false, err
	}
	return parsed.Op == agentueui.OpEnd, nil
}
