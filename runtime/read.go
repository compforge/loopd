package runtime

import (
	"github.com/compforge/loopd/runtime/model"
)

type Message = model.Message
type Stream = model.Stream
type MessageInfo = model.MessageInfo
type MessageSnapshot = model.MessageSnapshot
type BlockSnapshot = model.BlockSnapshot
type MessageQuery = model.MessageQuery
type MessageOrder = model.MessageOrder
type MessagePage = model.MessagePage

const (
	Asc  = model.Asc
	Desc = model.Desc
)
