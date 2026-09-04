package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/sse"
	loopd "github.com/compforge/loopd"
	"gorm.io/gorm"
)

type agentUEModel struct {
	Version string         `json:"version"`
	Biz     string         `json:"biz"`
	Meta    map[string]any `json:"meta"`
	Blocks  []any          `json:"blocks"`
}

type agentUEEvent struct {
	Op        string         `json:"op"`
	Seq       uint64         `json:"seq"`
	Timestamp int64          `json:"ts,omitempty"`
	Mask      string         `json:"mask,omitempty"`
	EventType string         `json:"event_type,omitempty"`
	Model     *agentUEModel  `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	Block     any            `json:"block,omitempty"`
}

func (server *Server) streamInvocation(ctx context.Context, request *hertzapp.RequestContext) error {
	invocationID := request.Param("invocation_id")
	model, invocation, cursor, err := server.invocationModel(ctx, invocationID)
	if err != nil {
		return err
	}
	request.Header("X-Accel-Buffering", "no")
	writer := sse.NewWriter(request)
	defer writer.Close()
	if err := writeAgentUE(writer, cursor, agentUEEvent{Op: "start", Seq: cursor, Model: &model}); err != nil {
		return nil
	}
	if invocation.Phase.Terminal() {
		return finishAgentUE(writer, cursor, invocation)
	}

	poll := time.NewTicker(server.streamPoll)
	ping := time.NewTicker(server.streamPing)
	defer poll.Stop()
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			if err := writeAgentUE(writer, cursor, agentUEEvent{
				Op: "ping", Seq: cursor, Timestamp: time.Now().UnixMilli(),
			}); err != nil {
				return nil
			}
		case <-poll.C:
			events, err := server.store.ListInvocationEvents(ctx, invocationID, cursor, maxPageSize)
			if err != nil {
				server.logger.Error("poll persisted Invocation events", "invocation_id", invocationID, "error", err)
				return nil
			}
			for _, event := range events {
				patch := invocationEventPatch(event, invocation.Responder.Role == loopd.RoleHarness)
				if err := writeAgentUE(writer, event.Cursor, patch); err != nil {
					return nil
				}
				cursor = event.Cursor
			}
			if len(events) == 0 {
				continue
			}
			invocation, err = server.store.GetInvocation(ctx, invocationID)
			if err != nil {
				return nil
			}
			if invocation.Phase.Terminal() {
				return finishAgentUE(writer, cursor, invocation)
			}
		}
	}
}

func (server *Server) invocationModel(ctx context.Context, id string) (agentUEModel, loopd.Invocation, uint64, error) {
	ctx, cancel := server.store.withTimeout(ctx)
	defer cancel()

	var model agentUEModel
	var invocation loopd.Invocation
	var cursor uint64
	err := server.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invocationRow invocationPO
		if err := tx.First(&invocationRow, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		invocation = invocationFromPO(invocationRow)

		var inputRow messagePO
		if err := tx.First(&inputRow, "id = ?", invocation.InputMessageID).Error; err != nil {
			return mapDBError(err)
		}
		var activityRows []activityPO
		if err := tx.Where("invocation_id = ?", id).Order("created_at ASC").Find(&activityRows).Error; err != nil {
			return err
		}
		var callRows []harnessCallPO
		if err := tx.Where("invocation_id = ?", id).Order("created_at ASC").Find(&callRows).Error; err != nil {
			return err
		}
		var interactionRows []interactionPO
		if err := tx.Where("invocation_id = ?", id).Order("created_at ASC").Find(&interactionRows).Error; err != nil {
			return err
		}
		var cursorRow invocationEventPO
		err := tx.Where("invocation_id = ?", id).Order("cursor DESC").First(&cursorRow).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			cursor = cursorRow.Cursor
		}

		blocks := []any{map[string]any{
			"id": "query", "type": "text", "role": loopd.RoleUser,
			"content": messageFromPO(inputRow).Content,
		}}
		if invocation.OutputMessageID != "" {
			var outputRow messagePO
			if err := tx.First(&outputRow, "id = ?", invocation.OutputMessageID).Error; err != nil {
				return mapDBError(err)
			}
			message := messageFromPO(outputRow)
			blocks = append(blocks, map[string]any{
				"id": "answer", "type": "text", "role": message.Role, "content": message.Content,
			})
		} else if invocation.Responder.Role == loopd.RoleHarness && len(callRows) > 0 && callRows[0].StreamText != "" {
			blocks = append(blocks, map[string]any{
				"id": "answer", "type": "text", "role": loopd.RoleHarness, "content": callRows[0].StreamText,
			})
		}
		for _, row := range activityRows {
			activity := activityFromPO(row)
			blocks = append(blocks, map[string]any{
				"id": activity.ID, "type": "activity", "activity": activity,
			})
		}
		for _, row := range callRows {
			call := callFromPO(row)
			blocks = append(blocks, map[string]any{
				"id": call.ID, "type": "harness_call", "call": call,
			})
		}
		for _, row := range interactionRows {
			interaction := interactionFromPO(row)
			blocks = append(blocks, map[string]any{
				"id": interaction.ID, "type": "interaction", "interaction": interaction,
			})
		}
		model = agentUEModel{
			Version: "1.0", Biz: "loopd.invocation",
			Meta: map[string]any{"invocation": invocation, "cursor": cursor}, Blocks: blocks,
		}
		return nil
	})
	if err != nil {
		return agentUEModel{}, loopd.Invocation{}, 0, err
	}
	return model, invocation, cursor, nil
}

func invocationEventPatch(event loopd.InvocationEvent, directHarness bool) agentUEEvent {
	if event.Kind == "invocation.completed" {
		var completed struct {
			Message loopd.Message `json:"message"`
		}
		if json.Unmarshal(event.Data, &completed) == nil && completed.Message.ID != "" {
			return agentUEEvent{
				Op: "set", Seq: event.Cursor, Timestamp: event.CreatedAt.UnixMilli(),
				EventType: event.Kind,
				Block: map[string]any{
					"id": "answer", "type": "text", "role": completed.Message.Role,
					"content": completed.Message.Content,
				},
			}
		}
	}
	if directHarness && event.Kind == "harness.message.delta" {
		if delta := harnessTextDelta(event.Data); delta != "" {
			return agentUEEvent{
				Op: "append", Seq: event.Cursor, Timestamp: event.CreatedAt.UnixMilli(),
				Mask: "block.content", EventType: "message.delta",
				Block: map[string]any{
					"id": "answer", "type": "text", "role": loopd.RoleHarness, "content": delta,
				},
			}
		}
	}
	var payload any
	if len(event.Data) > 0 && json.Unmarshal(event.Data, &payload) != nil {
		payload = string(event.Data)
	}
	return agentUEEvent{
		Op: "set", Seq: event.Cursor, Timestamp: event.CreatedAt.UnixMilli(),
		EventType: event.Kind,
		Block: map[string]any{
			"id":   "event-" + strconv.FormatUint(event.Cursor, 10),
			"type": "loop_event", "event_type": event.Kind, "data": payload,
		},
	}
}

func finishAgentUE(writer *sse.Writer, cursor uint64, invocation loopd.Invocation) error {
	if invocation.Phase == loopd.InvocationFailed || invocation.Phase == loopd.InvocationCancelled {
		cursor++
		if err := writeAgentUE(writer, cursor, agentUEEvent{
			Op: "error", Seq: cursor, Mask: "meta.error",
			Meta: map[string]any{"error": map[string]any{
				"code": string(invocation.Phase), "message": invocation.Error,
			}},
		}); err != nil {
			return err
		}
	}
	return writeAgentUE(writer, cursor, agentUEEvent{Op: "end", Seq: cursor})
}

func writeAgentUE(writer *sse.Writer, cursor uint64, event agentUEEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode AgentUE event: %w", err)
	}
	return writer.WriteEvent(strconv.FormatUint(cursor, 10), "agentue", encoded)
}
