package service

import (
	"context"
	"encoding/json"
	"strings"

	loopd "github.com/compforge/loopd"
)

// MessageView is a page projection. Cards and references are never persisted.
type MessageView struct {
	loopd.Message
	Card    MessageCard       `json:"card"`
	ReplyTo *MessageReference `json:"reply_to,omitempty"`
}
type MessageReference struct {
	ID      string          `json:"id"`
	Kind    loopd.ActorKind `json:"kind"`
	Key     string          `json:"key"`
	Preview string          `json:"preview"`
}
type MessageCard struct {
	Type          string            `json:"type"`
	Mode          string            `json:"mode,omitempty"`
	QuestionID    string            `json:"question_id,omitempty"`
	Question      *loopd.HumanBlock `json:"question,omitempty"`
	SelectedValue *string           `json:"selected_value,omitempty"`
	ReplyID       string            `json:"reply_id,omitempty"`
	Editable      bool              `json:"editable"`
}

type humanAnswer struct {
	Type    string            `json:"type"`
	Outcome loopd.HumanStatus `json:"outcome"`
	Value   string            `json:"value"`
}

func questionBlock(m loopd.Message) *loopd.HumanBlock {
	if m.Purpose != "human_request" {
		return nil
	}
	var content struct {
		Blocks []loopd.HumanBlock `json:"blocks"`
	}
	if json.Unmarshal(m.Content, &content) != nil || len(content.Blocks) != 1 {
		return nil
	}
	q := content.Blocks[0]
	if q.Type != "ask" && q.Type != "confirm" {
		return nil
	}
	return &q
}
func answerBlock(m loopd.Message) *humanAnswer {
	if m.Purpose != "human_reply" || m.Kind != loopd.ActorKindUser {
		return nil
	}
	var content struct {
		Blocks []humanAnswer `json:"blocks"`
	}
	if json.Unmarshal(m.Content, &content) != nil || len(content.Blocks) != 1 {
		return nil
	}
	a := content.Blocks[0]
	if a.Type != "human_reply" || (a.Outcome != loopd.HumanSuccess && a.Outcome != loopd.HumanDismissed) {
		return nil
	}
	return &a
}
func isAnswer(question, answer loopd.Message) bool {
	return answer.ConversationID == question.ConversationID && answer.ReplyToID == question.ID &&
		question.TargetKind == loopd.ActorKindUser && answer.Kind == loopd.ActorKindUser && question.TargetKey == answer.Key &&
		answer.TargetKind == question.Kind && answer.TargetKey == question.Key && answerBlock(answer) != nil
}

// EnrichMessages keeps page membership, order and revisions unchanged. Only direct
// references are read, in batches scoped to each visible conversation.
// +spec=`富化不改变分页；引用跨页按 reply_to_id 读取，不能跨会话或递归展开，也不能用普通回复伪造 Human 选择`
func (s *MessageService) EnrichMessages(ctx context.Context, messages []loopd.Message) ([]MessageView, error) {
	views := make([]MessageView, len(messages))
	groups := map[string][]int{}
	for i, m := range messages {
		groups[m.ConversationID] = append(groups[m.ConversationID], i)
	}
	for convID, indices := range groups {
		known := map[string]loopd.Message{}
		for _, i := range indices {
			known[messages[i].ID] = messages[i]
		}
		missing := map[string]bool{}
		for _, i := range indices {
			id := messages[i].ReplyToID
			if _, exists := known[id]; id != "" && !exists {
				missing[id] = true
			}
		}
		ids := make([]string, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			rows, err := s.repo.GetMessages(ctx, convID, ids)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if row.ConversationID == convID {
					known[row.ID] = messageFromModel(row)
				}
			}
		}
		answers := map[string]loopd.Message{}
		for _, m := range known {
			if q, ok := known[m.ReplyToID]; ok && isAnswer(q, m) {
				answers[q.ID] = m
			}
		}
		// Resolved questions retain their choice even when the answer is on another page.
		ids = nil
		for _, i := range indices {
			m := messages[i]
			if q := questionBlock(m); q != nil && (q.Status == loopd.HumanSuccess || q.Status == loopd.HumanDismissed) {
				if _, found := answers[m.ID]; !found {
					ids = append(ids, m.ID)
				}
			}
		}
		if len(ids) > 0 {
			rows, err := s.repo.ListHumanReplies(ctx, convID, ids)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				m := messageFromModel(row)
				if q, ok := known[m.ReplyToID]; ok && isAnswer(q, m) {
					answers[q.ID] = m
				}
			}
		}
		for _, i := range indices {
			m := messages[i]
			v := MessageView{Message: m, Card: MessageCard{Type: "content"}}
			original, found := known[m.ReplyToID]
			if found {
				v.ReplyTo = &MessageReference{ID: original.ID, Kind: original.Kind, Key: original.Key, Preview: messagePreview(original)}
			}
			if q := questionBlock(m); q != nil {
				v.Card = MessageCard{Type: q.Type, Mode: "request", QuestionID: m.ID, Question: q, Editable: q.Status == loopd.HumanPending}
				if a, ok := answers[m.ID]; ok && q.Status != loopd.HumanPending {
					applyAnswer(&v.Card, a)
				}
			} else if found && isAnswer(original, m) {
				if q := questionBlock(original); q != nil {
					v.Card = MessageCard{Type: q.Type, Mode: "reply", QuestionID: original.ID, Question: q}
					// A reply describes its own accepted result, even if a stale question snapshot was supplied.
					q.Status = answerBlock(m).Outcome
					applyAnswer(&v.Card, m)
				}
			}
			views[i] = v
		}
	}
	return views, nil
}
func applyAnswer(card *MessageCard, m loopd.Message) {
	a := answerBlock(m)
	if a == nil || a.Outcome != card.Question.Status {
		return
	}
	card.ReplyID = m.ID
	if a.Outcome == loopd.HumanSuccess {
		card.SelectedValue = &a.Value
	}
}
func messagePreview(m loopd.Message) string {
	var content struct {
		Blocks []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Value   string `json:"value"`
		} `json:"blocks"`
	}
	if json.Unmarshal(m.Content, &content) != nil {
		return ""
	}
	for _, b := range content.Blocks {
		for _, text := range []string{b.Title, b.Content, b.Value} {
			if text = strings.TrimSpace(text); text != "" {
				chars := []rune(text)
				if len(chars) > 120 {
					return string(chars[:120]) + "…"
				}
				return text
			}
		}
	}
	return ""
}
