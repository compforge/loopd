package service

import (
	"context"
	"encoding/json"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

const (
	defaultPageSize = 100
	maxPageSize     = 500
)

func (service *Service) CreateMessage(
	ctx context.Context,
	conversationID string,
	kind loopd.Role,
	key string,
	content json.RawMessage,
) (loopd.Message, error) {
	if !kind.Valid() || strings.TrimSpace(key) == "" || validateContent(content) != nil {
		return loopd.Message{}, ErrInvalid
	}
	message, err := service.repo.CreateMessage(ctx, model.Message{
		ID:             uuid.V7(),
		ConversationID: conversationID,
		Kind:           string(kind),
		Key:            strings.TrimSpace(key),
		Content:        content,
	})
	return messageFromModel(message), err
}

func (service *Service) ListMessages(ctx context.Context, conversationID, after string, limit int) ([]loopd.Message, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	rows, err := service.repo.ListMessages(ctx, conversationID, after, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]loopd.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, messageFromModel(row))
	}
	return messages, nil
}

func messageFromModel(value model.Message) loopd.Message {
	return loopd.Message{
		ID: value.ID, ConversationID: value.ConversationID,
		Kind: loopd.Role(value.Kind), Key: value.Key, Content: json.RawMessage(value.Content), CreatedAt: value.CreatedAt,
	}
}

func validateContent(content json.RawMessage) error {
	var model struct {
		Version string            `json:"version"`
		Biz     string            `json:"biz"`
		Meta    json.RawMessage   `json:"meta"`
		Blocks  []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(content, &model); err != nil {
		return err
	}
	if model.Version == "" || model.Biz == "" || len(model.Meta) == 0 || model.Blocks == nil {
		return ErrInvalid
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(model.Meta, &meta); err != nil {
		return err
	}
	blockIDs := make(map[string]struct{}, len(model.Blocks))
	for _, rawBlock := range model.Blocks {
		var block struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return err
		}
		if block.ID == "" || block.Type == "" {
			return ErrInvalid
		}
		if _, exists := blockIDs[block.ID]; exists {
			return ErrInvalid
		}
		blockIDs[block.ID] = struct{}{}
	}
	return nil
}
