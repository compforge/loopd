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

type Delivery struct {
	client *client
	state  *deliveryState
}

type deliveryState struct {
	mu        sync.Mutex
	sequences map[string]*messageSequence
}

type messageSequence struct {
	mu   sync.Mutex
	next uint64
}

func newDelivery(client *client) Delivery {
	return Delivery{client: client, state: &deliveryState{sequences: make(map[string]*messageSequence)}}
}

type CreateConversationRequest struct {
	Name string `json:"name"`
}

type SendMessageRequest struct {
	TaskID  string          `json:"task_id,omitempty"`
	UserKey string          `json:"user_key,omitempty"`
	Target  loopd.ActorRef  `json:"target,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type DeliveryFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (chat Delivery) CreateConversation(
	ctx context.Context,
	request CreateConversationRequest,
) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := chat.client.do(ctx, http.MethodPost, "/v1/conversations", request, &result)
	return result, err
}

func (chat Delivery) Conversation(ctx context.Context, conversationID string) (loopd.Conversation, error) {
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

// Send submits a user message without TaskID, or observes its UI delivery with TaskID.
// Closing the HTTP stream does not cancel any actor's work.
func (chat Delivery) Send(
	ctx context.Context,
	conversationID string,
	request SendMessageRequest,
	lastEventID string,
) (*DeliveryStream, error) {
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
	stream := &DeliveryStream{
		taskID:      taskID,
		body:        response.Body,
		scanner:     bufio.NewScanner(response.Body),
		lastEventID: lastEventID,
	}
	stream.scanner.Buffer(make([]byte, 64<<10), 16<<20)
	return stream, nil
}

// DeliveryStream reads one AgentUE event at a time from the Delivery SSE response.
type DeliveryStream struct {
	taskID      string
	body        io.ReadCloser
	scanner     *bufio.Scanner
	lastEventID string
}

func (stream *DeliveryStream) TaskID() string { return stream.taskID }

func (stream *DeliveryStream) Next() (loopd.Event, error) {
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

func (stream *DeliveryStream) Close() error { return stream.body.Close() }

func (chat Delivery) History(
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

// EmitMessage is a Verb (effect: write). Equal block IDs and sequence numbers in other
// messages never collide. Message identity is independent of a UI delivery.
func (chat Delivery) EmitMessage(ctx context.Context, messageID string, event agentueui.Event) (loopd.Event, error) {
	result, err := chat.emit(ctx, "message/"+messageID, "/v1/messages/"+url.PathEscape(messageID)+"/events", event)
	result.MessageID = messageID
	return result, err
}

func (chat Delivery) emit(ctx context.Context, sequenceKey, path string, event agentueui.Event) (loopd.Event, error) {
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

func (chat Delivery) sequence(messageID string) *messageSequence {
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

// Complete is a Verb (effect: write) that closes a UI delivery, not its messages. Repeating the same completion is safe.
func (chat Delivery) Complete(ctx context.Context, taskID string, failure *DeliveryFailure) error {
	return chat.client.do(ctx, http.MethodPost, "/v1/deliveries/"+url.PathEscape(taskID)+"/complete", struct {
		Error *DeliveryFailure `json:"error,omitempty"`
	}{Error: failure}, nil)
}

// IsEnd reports UI delivery completion on a Delivery stream, not the end of a work message.
func IsEnd(event loopd.Event) (bool, error) {
	parsed, err := agentueui.Parse(event.Data)
	if err != nil {
		return false, err
	}
	return parsed.Op == agentueui.OpEnd && event.MessageID == "" && event.Message == nil, nil
}
