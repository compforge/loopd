package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runner "github.com/compforge/agentue/sdks/go/runner"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type countingSnapshots struct {
	*repo.Store
	reads int
}

func (s *countingSnapshots) GetMessage(ctx context.Context, id string) (model.Message, error) {
	s.reads++
	return s.Store.GetMessage(ctx, id)
}

// +case=`外置正文的普通流式更新不全量读取；桥丢失后恢复完整快照，不把 ref 交给页面 reducer`
func TestPartsStreamReadsBodyOnlyForSnapshotRepair(t *testing.T) {
	store, coordinator, _ := outputFixture(t)
	ctx := context.Background()
	request := outputRequest("large-output")
	request.Content = json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"text","type":"text","content":"` + strings.Repeat("x", 70<<10) + `"}]}`)
	message, err := store.Speak(ctx, "work", request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.GetMessageState(ctx, message.ID)
	if err != nil || state.Revision != message.Revision || state.Ended {
		t.Fatalf("message progress: %+v, %v", state, err)
	}
	if err := coordinator.ensureStream(ctx, message); err != nil {
		t.Fatal(err)
	}
	counts := &countingSnapshots{Store: store}
	writer := NewMessageService(counts, coordinator.events, nil)
	update := marshalEvent(t, ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "text", "content": " first"}})
	if _, err := writer.EmitMessage(ctx, message.ID, update); err != nil {
		t.Fatal(err)
	}
	if counts.reads != 0 {
		t.Fatalf("ordinary update read full body %d times", counts.reads)
	}
	if err := coordinator.events.Delete(ctx, streamKey(message)); err != nil {
		t.Fatal(err)
	}
	update = marshalEvent(t, ui.Event{Op: ui.OpAppend, Seq: 3, Mask: "block.content", Block: map[string]any{"id": "text", "content": " second"}})
	if _, err := writer.EmitMessage(ctx, message.ID, update); err != nil {
		t.Fatal(err)
	}
	if counts.reads != 1 {
		t.Fatalf("repair snapshot reads=%d", counts.reads)
	}
	watch, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err = (runner.Replayer{Bridge: coordinator.events}).Stream(watch, streamKey(message), "", func(d runner.Delivery) error {
		e, err := ui.Parse(d.Data)
		if err != nil {
			return err
		}
		if e.Op == ui.OpStart {
			if e.Seq != 3 {
				t.Fatalf("snapshot revision=%d", e.Seq)
			}
			block := e.Model["blocks"].([]any)[0].(map[string]any)
			if block["content"] != strings.Repeat("x", 70<<10)+" first second" {
				t.Fatal("replay lost or duplicated content")
			}
			if _, ref := block["ref"]; ref {
				t.Fatal("storage reference reached page")
			}
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatal(err)
	}
}

func TestOutputCannotForgeStorageReference(t *testing.T) {
	store, coordinator, _ := outputFixture(t)
	ctx := context.Background()
	message, err := store.Speak(ctx, "work", outputRequest("forged-reference"))
	if err != nil {
		t.Fatal(err)
	}
	data := marshalEvent(t, ui.Event{Op: ui.OpSet, Seq: 2, Block: map[string]any{"id": "b2", "ref": "another-message-part"}})
	if _, err := coordinator.EmitMessage(ctx, message.ID, data); !errors.Is(err, repo.ErrInvalidContent) {
		t.Fatalf("external reference accepted: %v", err)
	}
	saved, err := store.GetMessage(ctx, message.ID)
	if err != nil || saved.Revision != message.Revision || string(saved.Content) != string(message.Content) {
		t.Fatalf("rejected input changed message: %+v, %v", saved, err)
	}
}
