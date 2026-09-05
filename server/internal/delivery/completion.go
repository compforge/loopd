package delivery

import (
	"context"
	"fmt"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
)

func (coordinator *Coordinator) finishMessage(ctx context.Context, message model.Message, failure *Failure) error {
	streamID := streamKey(message)
	if err := coordinator.ensureStream(ctx, message); err != nil {
		return err
	}
	state, err := coordinator.events.State(ctx, streamID)
	if err != nil {
		return err
	}
	if state.Status.Terminal() {
		return nil
	}
	conversationID, messageID := message.ConversationID, message.ID
	values, err := coordinator.events.EventsThrough(ctx, streamID, "")
	if err != nil {
		return err
	}
	snapshot := map[string]any{}
	lastSeq := uint64(0)
	lastOp := agentueui.Op("")
	hasFailure := false
	for _, value := range values {
		event, parseErr := agentueui.Parse(value.Data)
		if parseErr != nil {
			return fmt.Errorf("rebuild stream %q at cursor %q: %w", streamID, value.Cursor, parseErr)
		}
		if event.Op != agentueui.OpPing && event.Seq <= lastSeq {
			return fmt.Errorf("stream %q AgentUE sequence did not increase", streamID)
		}
		snapshot, err = agentueui.Apply(snapshot, event)
		if err != nil {
			return fmt.Errorf("rebuild stream %q at cursor %q: %w", streamID, value.Cursor, err)
		}
		if event.Op != agentueui.OpPing {
			lastSeq = event.Seq
		}
		if event.Op == agentueui.OpError {
			hasFailure = true
		}
		lastOp = event.Op
	}
	status := agentuerunner.StatusCompleted
	if failure != nil && lastOp != agentueui.OpEnd && !hasFailure {
		status = agentuerunner.StatusFailed
		lastSeq++
		failed := agentueui.Failure(lastSeq, failure.Code, failure.Message)
		data, marshalErr := failed.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = coordinator.events.Publish(ctx, streamID, data, failed.Seq); err != nil {
			return err
		}
		snapshot, err = agentueui.Apply(snapshot, failed)
		if err != nil {
			return err
		}
		hasFailure = true
		lastOp = failed.Op
	}
	if hasFailure {
		status = agentuerunner.StatusFailed
	}
	content, err := agentueui.MarshalSnapshot(snapshot)
	if err != nil {
		return fmt.Errorf("marshal stream %q snapshot: %w", streamID, err)
	}
	if err := coordinator.repo.SaveOutput(ctx, messageID, content, lastSeq); err != nil {
		return fmt.Errorf("persist stream %q snapshot: %w", streamID, err)
	}
	if lastOp != agentueui.OpEnd {
		lastSeq++
		end := agentueui.End(lastSeq)
		data, marshalErr := end.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = coordinator.events.Publish(ctx, streamID, data, end.Seq); err != nil {
			return err
		}
	}
	if err := coordinator.events.MarkTerminal(ctx, streamID, status); err != nil {
		return err
	}
	coordinator.logger.InfoContext(ctx, "message output completed",
		"task_id", message.TaskID,
		"conversation_id", conversationID,
		"message_id", messageID,
		"status", status,
	)
	return nil
}
