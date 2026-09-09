package verb

import (
	"context"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/model"
	"github.com/compforge/loopd/runtime/service"
)

// Conv exposes persistent collaboration without Server transport details.
type Conv struct{ service service.Conv }

func NewConv(s service.Conv) Conv { return Conv{service: s} }

// Poll records receipt without committing consumption. While working, pass the last
// successful Position as After; on recovery omit After to replay uncommitted inputs.
// +spec=`Poll returns messages in every status, including streaming. Processing partial content and deciding when to Commit belong to the Operator.`
// Position tracks IDs, not revisions. Retain the Message ID and use Read (or a
// Harness Call) to follow later content after advancing past a streaming message.
func (c Conv) Poll(ctx context.Context, id string, request contract.PollRequest) (contract.PollResult, error) {
	return c.service.Poll(ctx, id, request)
}

// Commit acknowledges a contiguous safely handled prefix, not business completion.
func (c Conv) Commit(ctx context.Context, id string, request contract.CommitRequest) error {
	return c.service.Commit(ctx, id, request)
}

// Read creates a lazy reference. Only an explicit data read performs I/O.
func (c Conv) Read(conversationID, messageID string) model.Message {
	return c.service.Read(conversationID, messageID)
}

// List reads bounded metadata and returns lazy handles, not message bodies.
func (c Conv) List(ctx context.Context, id string, query model.MessageQuery) (model.MessagePage, error) {
	return c.service.List(ctx, id, query)
}

// Speak publishes complete content. Status may mark failure; use Tell for incremental output.
func (c Conv) Speak(ctx context.Context, id string, request contract.SpeakRequest) (model.Message, error) {
	if request.Status == "" {
		request.Status = contract.MessageStatusCompleted
	}
	if !request.Status.Terminal() {
		return nil, model.ErrSpeakStatusInvalid
	}
	info, err := c.service.Create(ctx, id, request)
	if err != nil {
		return nil, err
	}
	return c.service.Read(id, info.ID), nil
}

// Tell starts or restores a streaming message. Its writer owns Emit/End.
func (c Conv) Tell(ctx context.Context, id string, request contract.SpeakRequest) (model.Stream, error) {
	if request.Status != "" && request.Status != contract.MessageStatusStreaming {
		return nil, model.ErrTellStatusInvalid
	}
	request.Status = contract.MessageStatusStreaming
	info, err := c.service.Create(ctx, id, request)
	if err != nil {
		return nil, err
	}
	return c.service.Stream(info), nil
}
