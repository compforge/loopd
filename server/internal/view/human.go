package view

import "github.com/compforge/loopd/pkg/contract"

type HumanResult struct {
	contract.HumanResult
	Message Message  `json:"message"`
	Reply   *Message `json:"reply,omitempty"`
}
