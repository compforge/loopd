package view

import loopd "github.com/compforge/loopd"

type HumanResult struct {
	loopd.HumanResult
	Message Message  `json:"message"`
	Reply   *Message `json:"reply,omitempty"`
}
