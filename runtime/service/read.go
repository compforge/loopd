package service

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
)

type remoteMessage struct {
	client     *infra.Client
	convID, id string
}

func (m *remoteMessage) ID() string             { return m.id }
func (m *remoteMessage) ConversationID() string { return m.convID }
func (m *remoteMessage) path() string {
	return "/v1/conversations/" + url.PathEscape(m.convID) + "/messages/" + url.PathEscape(m.id)
}
func (m *remoteMessage) Info(ctx context.Context) (model.MessageInfo, error) {
	var value model.MessageInfo
	err := m.client.Do(ctx, http.MethodGet, m.path(), nil, &value)
	return value, err
}
func (m *remoteMessage) Snapshot(ctx context.Context) (model.MessageSnapshot, error) {
	var value model.MessageSnapshot
	err := m.client.Do(ctx, http.MethodGet, m.path()+"/content", nil, &value)
	return value, err
}
func (m *remoteMessage) Block(ctx context.Context, id string) (model.BlockSnapshot, error) {
	var value model.BlockSnapshot
	err := m.client.Do(ctx, http.MethodGet, m.path()+"/blocks/"+url.PathEscape(id), nil, &value)
	return value, err
}

// Blocks visits logical blocks at one revision. A changed revision returns conflict;
// callers must discard partial results and retry, rather than mix message versions.
func (m *remoteMessage) Blocks(ctx context.Context, visit func(model.BlockSnapshot) error) error {
	cursor := ""
	for {
		var page contract.BlockPage
		if err := m.client.Do(ctx, http.MethodGet, m.path()+"/blocks?cursor="+url.QueryEscape(cursor), nil, &page); err != nil {
			return err
		}
		for _, block := range page.Data {
			if err := visit(model.BlockSnapshot{Revision: page.Revision, Block: block}); err != nil {
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
func (conv Conv) Read(conversationID, messageID string) model.Message {
	return &remoteMessage{client: conv.client, convID: conversationID, id: messageID}
}

// List loads bounded metadata only. Before/After are exclusive message IDs.
func (conv Conv) List(ctx context.Context, conversationID string, query model.MessageQuery) (model.MessagePage, error) {
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
	err := conv.client.Do(ctx, http.MethodGet, "/v1/conversations/"+url.PathEscape(conversationID)+"/messages?"+q.Encode(), nil, &value)
	if err != nil {
		return model.MessagePage{}, err
	}
	result := model.MessagePage{Next: value.Next, Infos: value.Data, Messages: make([]model.Message, 0, len(value.Data))}
	for _, info := range value.Data {
		result.Messages = append(result.Messages, conv.Read(conversationID, info.ID))
	}
	return result, nil
}
