package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ResultBlockType = "result"

// HarnessResult is the final content identified by an Adapter, not execution status.
type HarnessResult struct {
	Format  string          `json:"format"`
	Content json.RawMessage `json:"content"`
}

func TextResult(text string) HarnessResult {
	data, _ := json.Marshal(text)
	return HarnessResult{Format: "text", Content: data}
}
func (r HarnessResult) Validate() error {
	if !json.Valid(r.Content) {
		return errors.New("result content must be valid JSON")
	}
	if r.Format == "json" {
		return nil
	}
	if r.Format == "text" {
		var text string
		if json.Unmarshal(r.Content, &text) == nil && string(r.Content) != "null" {
			return nil
		}
	}
	return errors.New("result format must be text with a string content, or json")
}
func (r *HarnessResult) Text() string {
	if r == nil {
		return ""
	}
	if r.Format == "text" {
		var text string
		_ = json.Unmarshal(r.Content, &text)
		return text
	}
	return string(r.Content)
}
func (r HarnessResult) Block() (map[string]any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	var content any
	_ = json.Unmarshal(r.Content, &content)
	return map[string]any{"id": "result", "type": ResultBlockType, "format": r.Format, "content": content}, nil
}

// ExtractResult reads an explicit final block from a fully hydrated Message.
func ExtractResult(content json.RawMessage) (*HarnessResult, error) {
	var snapshot struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return nil, err
	}
	var result *HarnessResult
	for _, raw := range snapshot.Blocks {
		var b struct {
			Type string `json:"type"`
			HarnessResult
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		if b.Type != ResultBlockType {
			continue
		}
		if result != nil {
			return nil, errors.New("multiple result blocks")
		}
		if err := b.Validate(); err != nil {
			return nil, fmt.Errorf("invalid result block: %w", err)
		}
		v := b.HarnessResult
		result = &v
	}
	return result, nil
}

type HarnessRunRequest struct {
	ConversationID string         `json:"conversation_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	EffectKey      string         `json:"effect_key"`
	Target         string         `json:"target"`
	Text           string         `json:"text"`
	Tools          []Tool         `json:"tools,omitempty"`
	Actor          *ActorRef      `json:"actor,omitempty"`
	Recipient      ActorRef       `json:"recipient,omitempty"`
	Timeout        time.Duration  `json:"timeout"`
	Meta           map[string]any `json:"meta,omitempty"`
}

type HarnessObservation struct {
	Call    HarnessCall `json:"call"`
	Message Message     `json:"message"`
}
