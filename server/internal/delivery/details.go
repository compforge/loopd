package delivery

import (
	"context"
	"fmt"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

func (coordinator *Coordinator) ensureDetail(ctx context.Context, response model.Message, callID string, block map[string]any) (model.Message, error) {
	// The temporary Harness is identified by its Call, not by the shared
	// adapter name. Registered Harness identity can be added at that boundary.
	effectKey, _ := block["effect_key"].(string)
	content, err := agentueui.MarshalSnapshot(map[string]any{
		"version": "1.0", "biz": "chat", "meta": map[string]any{"effect_key": effectKey}, "blocks": []any{},
	})
	if err != nil {
		return model.Message{}, err
	}
	message, created, err := coordinator.repo.EnsureDetailMessage(ctx,
		model.Conversation{ID: uuid.V7(), Name: "处理详情", ParentMessageID: &response.ID},
		model.Message{ID: uuid.V7(), TaskID: response.TaskID, Kind: string(loopd.RoleHarness), ActorKey: callID, Content: content},
	)
	if err == nil && created {
		coordinator.logger.InfoContext(ctx, "detail message created", "parent_message_id", response.ID,
			"conversation_id", message.ConversationID, "message_id", message.ID,
			"task_id", response.TaskID, "effect_key", effectKey)
	}
	return message, err
}

// persistDetails separates visible ownership without changing the task-wide
// AgentUE stream: the stream is transport, not the main Message's content.
func (coordinator *Coordinator) persistDetails(ctx context.Context, response model.Message, snapshot map[string]any) (map[string]any, error) {
	if response.Kind != string(loopd.RoleOperator) {
		return snapshot, nil
	}
	blocks, _ := snapshot["blocks"].([]any)
	mainBlocks := make([]any, 0, len(blocks))
	grouped := make(map[string][]any)
	var callIDs []string
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		callID, _ := block["call_id"].(string)
		if callID == "" {
			mainBlocks = append(mainBlocks, value)
			continue
		}
		if _, exists := grouped[callID]; !exists {
			callIDs = append(callIDs, callID)
		}
		grouped[callID] = append(grouped[callID], value)
	}
	for _, callID := range callIDs {
		block := grouped[callID][0].(map[string]any)
		message, err := coordinator.ensureDetail(ctx, response, callID, block)
		if err != nil {
			return nil, err
		}
		effectKey, _ := block["effect_key"].(string)
		content, err := agentueui.MarshalSnapshot(map[string]any{
			"version": snapshot["version"], "biz": snapshot["biz"],
			"meta": map[string]any{"effect_key": effectKey}, "blocks": grouped[callID],
		})
		if err != nil {
			return nil, err
		}
		if _, err := coordinator.repo.UpdateMessageContent(ctx, message.ConversationID, message.ID, content); err != nil {
			return nil, fmt.Errorf("persist detail message %q: %w", message.ID, err)
		}
	}
	snapshot["blocks"] = mainBlocks
	return snapshot, nil
}
