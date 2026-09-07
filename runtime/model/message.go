package model

import (
	"context"

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
