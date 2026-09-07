package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
)

type MessageInfo = contract.MessageInfo
type MessageSnapshot = contract.Message
type BlockSnapshot = contract.BlockSnapshot
type MessageQuery = contract.MessageQuery
type MessageOrder = contract.MessageOrder

const (
	Asc  = contract.MessageAsc
	Desc = contract.MessageDesc
)

// Message is a lazy read capability, not ownership of the writer or its execution.
// ID access never performs I/O; every data read returns the current server snapshot.
// +spec=`Message exposes logical content only; physical Parts never enter Operator reads or handles.`
type Message interface {
	ID() string
	ConversationID() string
	Info(context.Context) (MessageInfo, error)
	Snapshot(context.Context) (MessageSnapshot, error)
	Block(context.Context, string) (BlockSnapshot, error)
	Blocks(context.Context, func(BlockSnapshot) error) error
}

type Stream interface {
	Message
	Emit(context.Context, ui.Event) error
	End(context.Context, ...contract.MessageStatus) error
}

type MessagePage struct {
	Messages []Message
	Infos    []MessageInfo // metadata observed by List; no implicit cached reads
	Next     string
}

type remoteMessage struct {
	client     *client
	convID, id string
}

func (m *remoteMessage) ID() string             { return m.id }
func (m *remoteMessage) ConversationID() string { return m.convID }
func (m *remoteMessage) path() string {
	return "/v1/conversations/" + url.PathEscape(m.convID) + "/messages/" + url.PathEscape(m.id)
}
func (m *remoteMessage) Info(ctx context.Context) (MessageInfo, error) {
	var value MessageInfo
	err := m.client.do(ctx, http.MethodGet, m.path(), nil, &value)
	return value, err
}
func (m *remoteMessage) Snapshot(ctx context.Context) (MessageSnapshot, error) {
	var value MessageSnapshot
	err := m.client.do(ctx, http.MethodGet, m.path()+"/content", nil, &value)
	return value, err
}
func (m *remoteMessage) Block(ctx context.Context, id string) (BlockSnapshot, error) {
	var value BlockSnapshot
	err := m.client.do(ctx, http.MethodGet, m.path()+"/blocks/"+url.PathEscape(id), nil, &value)
	return value, err
}

// Blocks visits logical blocks at one revision. A changed revision returns conflict;
// callers must discard partial results and retry, rather than mix message versions.
func (m *remoteMessage) Blocks(ctx context.Context, visit func(BlockSnapshot) error) error {
	cursor := ""
	for {
		var page contract.BlockPage
		if err := m.client.do(ctx, http.MethodGet, m.path()+"/blocks?cursor="+url.QueryEscape(cursor), nil, &page); err != nil {
			return err
		}
		for _, block := range page.Data {
			if err := visit(BlockSnapshot{Revision: page.Revision, Block: block}); err != nil {
				return err
			}
		}
		if page.Next == "" {
			return nil
		}
		cursor = page.Next
	}
}

// Read creates an unchecked reference. Existence is resolved only on an explicit data read.
func (conv Conv) Read(conversationID, messageID string) Message {
	return &remoteMessage{client: conv.client, convID: conversationID, id: messageID}
}

// List loads bounded metadata only. Before/After are exclusive message IDs.
func (conv Conv) List(ctx context.Context, conversationID string, query MessageQuery) (MessagePage, error) {
	q := url.Values{"limit": {strconv.Itoa(query.Limit)}}
	if query.Limit == 0 {
		q.Del("limit")
	}
	if query.Before != "" {
		q.Set("before", query.Before)
	}
	if query.After != "" {
		q.Set("after", query.After)
	}
	if query.Order != "" {
		q.Set("order", string(query.Order))
	}
	if len(query.IDs) > 0 {
		q.Set("ids", strings.Join(query.IDs, ","))
	}
	for _, status := range query.Statuses {
		q.Add("status", string(status))
	}
	var value contract.MessagePage
	err := conv.client.do(ctx, http.MethodGet, "/v1/conversations/"+url.PathEscape(conversationID)+"/messages?"+q.Encode(), nil, &value)
	if err != nil {
		return MessagePage{}, err
	}
	result := MessagePage{Next: value.Next, Infos: value.Data, Messages: make([]Message, 0, len(value.Data))}
	for _, info := range value.Data {
		result.Messages = append(result.Messages, conv.Read(conversationID, info.ID))
	}
	return result, nil
}
