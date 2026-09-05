package loopd

import (
	"fmt"
	"strings"
	"time"
)

type HumanStatus string

const (
	HumanPending   HumanStatus = "pending"
	HumanSuccess   HumanStatus = "success"
	HumanDismissed HumanStatus = "dismissed"
	HumanTimeout   HumanStatus = "timeout"
	HumanFailure   HumanStatus = "failure"
)

func (s HumanStatus) Terminal() bool { return s != HumanPending && s != "" }

type HumanChoice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// HumanRequest is the immutable input to one message-backed Human effect.
// Timeout uses Go duration nanoseconds on the JSON wire.
type HumanRequest struct {
	ConversationID string        `json:"conversation_id,omitempty"`
	Actor          ActorRef      `json:"actor,omitempty"`
	Target         ActorRef      `json:"target,omitempty"`
	ReplyToID      string        `json:"reply_to_id,omitempty"`
	TaskID         string        `json:"task_id"`
	EffectKey      string        `json:"effect_key"`
	Type           string        `json:"type"`
	Title          string        `json:"title"`
	Prompt         string        `json:"prompt"`
	Timeout        time.Duration `json:"timeout"`
	Choices        []HumanChoice `json:"choices"`
	AllowOther     bool          `json:"allow_other,omitempty"`
	ConfirmLabel   string        `json:"confirm_label,omitempty"`
	DeclineLabel   string        `json:"decline_label,omitempty"`
}

// +spec=`Timeout 必须为有限正值；Ask 支持单选、单选加自由文本或纯自由文本`
func (r HumanRequest) Validate() error {
	if strings.TrimSpace(r.ConversationID) == "" || strings.TrimSpace(r.EffectKey) == "" || strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Prompt) == "" || r.Timeout <= 0 {
		return fmt.Errorf("conversation_id, effect_key, title, prompt and positive timeout are required")
	}
	if r.Actor.Kind != RoleOperator || r.Actor.Key == "" || r.Target.Kind != RoleUser || r.Target.Key == "" {
		return fmt.Errorf("conversation Human request requires an operator sender and user recipient")
	}
	switch r.Type {
	case "ask":
		if r.ConfirmLabel != "" || r.DeclineLabel != "" {
			return fmt.Errorf("ask does not accept confirmation labels")
		}
		if (r.Choices != nil && len(r.Choices) == 0) || (!r.AllowOther && len(r.Choices) == 0) {
			return fmt.Errorf("ask requires choices or allow_other; explicit empty choices are invalid")
		}
		seen := map[string]bool{}
		for _, c := range r.Choices {
			if strings.TrimSpace(c.Value) == "" || strings.TrimSpace(c.Label) == "" || seen[c.Value] {
				return fmt.Errorf("choice values must be nonempty and unique; labels are required")
			}
			seen[c.Value] = true
		}
	case "confirm":
		if r.Choices != nil || r.AllowOther {
			return fmt.Errorf("confirm accepts only accepted or declined")
		}
	default:
		return fmt.Errorf("unknown Human action %q", r.Type)
	}
	return nil
}

type HumanReply struct {
	ReplyToID string      `json:"reply_to_id"`
	Outcome   HumanStatus `json:"outcome"`
	Value     string      `json:"value,omitempty"`
}

func (r HumanRequest) ValidateReply(reply HumanReply) error {
	if reply.ReplyToID == "" {
		return fmt.Errorf("reply_to_id is required")
	}
	if reply.Outcome == HumanDismissed && reply.Value == "" {
		return nil
	}
	if reply.Outcome != HumanSuccess {
		return fmt.Errorf("reply must be success(value) or dismissed")
	}
	if r.Type == "confirm" {
		if reply.Value == "accepted" || reply.Value == "declined" {
			return nil
		}
	} else {
		for _, c := range r.Choices {
			if reply.Value == c.Value {
				return nil
			}
		}
		if r.AllowOther && strings.TrimSpace(reply.Value) != "" {
			return nil
		}
	}
	return fmt.Errorf("answer does not match the declared Human request")
}

// HumanBlock is a loopd block extension; it introduces no AgentUE operations.
type HumanBlock struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Title        string        `json:"title"`
	Prompt       string        `json:"prompt"`
	Choices      []HumanChoice `json:"choices,omitempty"`
	AllowOther   bool          `json:"allow_other,omitempty"`
	ConfirmLabel string        `json:"confirm_label,omitempty"`
	DeclineLabel string        `json:"decline_label,omitempty"`
	Status       HumanStatus   `json:"status"`
	Deadline     time.Time     `json:"deadline"`
	Reason       string        `json:"reason,omitempty"`
}

type HumanResult struct {
	Message  Message     `json:"message"`
	Reply    *Message    `json:"reply,omitempty"`
	Status   HumanStatus `json:"status"`
	Value    string      `json:"value,omitempty"`
	Reason   string      `json:"reason,omitempty"`
	Deadline time.Time   `json:"deadline"`
}
