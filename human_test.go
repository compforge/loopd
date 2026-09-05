package loopd

import (
	"testing"
	"time"
)

func TestHumanRequestModes(t *testing.T) {
	base := HumanRequest{ConversationID: "conv", Actor: ActorRef{Kind: RoleOperator, Key: "router"}, Target: ActorRef{Kind: RoleUser, Key: "alice"}, TaskID: "task", EffectKey: "effect", Type: "ask", Title: "Scope", Prompt: "Choose", Timeout: time.Minute, Choices: []HumanChoice{{Value: "small", Label: "Small"}}}
	for _, test := range []struct {
		name   string
		change func(*HumanRequest)
		valid  bool
	}{
		{"choices", func(*HumanRequest) {}, true},
		{"no timeout", func(r *HumanRequest) { r.Timeout = 0 }, false},
		{"negative timeout", func(r *HumanRequest) { r.Timeout = -1 }, false},
		{"no options", func(r *HumanRequest) { r.Choices = nil }, false},
		{"free text", func(r *HumanRequest) { r.Choices = nil; r.AllowOther = true }, true},
		{"explicit empty", func(r *HumanRequest) { r.Choices = []HumanChoice{}; r.AllowOther = true }, false},
		{"other", func(r *HumanRequest) { r.AllowOther = true }, true},
		{"duplicate", func(r *HumanRequest) { r.Choices = append(r.Choices, r.Choices[0]) }, false},
		{"blank label", func(r *HumanRequest) { r.Choices = []HumanChoice{{Value: "x"}} }, false},
		{"confirm", func(r *HumanRequest) {
			r.Type = "confirm"
			r.Choices = nil
			r.ConfirmLabel = "Deploy"
			r.DeclineLabel = "Skip"
		}, true},
		{"confirm options", func(r *HumanRequest) { r.Type = "confirm" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := base
			test.change(&r)
			if (r.Validate() == nil) != test.valid {
				t.Fatalf("request=%+v error=%v", r, r.Validate())
			}
		})
	}
	for _, test := range []struct {
		other bool
		value string
		valid bool
	}{{false, "small", true}, {false, "other", false}, {true, "other", true}, {true, " ", false}} {
		r := base
		r.AllowOther = test.other
		if err := r.ValidateReply(HumanReply{ReplyToID: "question", Outcome: HumanSuccess, Value: test.value}); (err == nil) != test.valid {
			t.Fatalf("%+v: %v", test, err)
		}
	}
}
