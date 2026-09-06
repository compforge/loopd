package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type countedMessageLookup struct {
	*repo.Store
	lookups       [][]string
	answerLookups int
}

func (s *countedMessageLookup) GetMessages(ctx context.Context, convID string, ids []string) ([]model.Message, error) {
	s.lookups = append(s.lookups, append([]string{}, ids...))
	return s.Store.GetMessages(ctx, convID, ids)
}
func (s *countedMessageLookup) ListHumanReplies(ctx context.Context, convID string, ids []string) ([]model.Message, error) {
	s.answerLookups++
	return s.Store.ListHumanReplies(ctx, convID, ids)
}
func TestMessageEnrichmentAcrossPagesAndStorageParts(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "cards.db"), MessageInlineBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"conv", "other"} {
		if _, err := store.CreateConversation(ctx, model.Conversation{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	lookup := &countedMessageLookup{Store: store}
	service := NewMessageService(lookup, nil)
	r := loopd.HumanRequest{ConversationID: "conv", Actor: loopd.ActorRef{Kind: "operator", Key: "interaction"}, Target: loopd.ActorRef{Kind: "user", Key: "alice"}, Type: "ask", EffectKey: "scope", Title: "Choose scope", Prompt: "Pick one", Choices: []loopd.HumanChoice{{Value: "brief", Label: "简要说明"}, {Value: "full", Label: "完整说明"}}, Timeout: time.Minute}
	question, err := store.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// A different question can intervene; adjacency has no meaning.
	r.EffectKey = "other-question"
	r.Title = "Unrelated"
	unrelated, err := store.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	answered, err := store.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: question.Message.ID, Outcome: loopd.HumanSuccess, Value: "brief"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListMessages(ctx, "conv", unrelated.Message.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(page)
	views, err := service.EnrichMessages(ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(page)
	if string(before) != string(after) || len(views) != 1 || views[0].ID != answered.Reply.ID {
		t.Fatalf("pagination or input changed: %+v", views)
	}
	card := views[0].Card
	if card.Type != "ask" || card.Mode != "reply" || card.Question.Title != "Choose scope" || card.Question.Choices[0].Label != "简要说明" || card.Editable || card.SelectedValue == nil || *card.SelectedValue != "brief" {
		t.Fatalf("reply card: %+v", card)
	}
	if views[0].ReplyTo == nil || views[0].ReplyTo.ID != question.Message.ID || len(lookup.lookups) != 1 || !reflect.DeepEqual(lookup.lookups[0], []string{question.Message.ID}) {
		t.Fatalf("direct lookup: %+v", lookup.lookups)
	}
	// The question's page does not include its later answer.
	lookup.lookups = nil
	lookup.answerLookups = 0
	page, err = service.ListMessages(ctx, "conv", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	views, err = service.EnrichMessages(ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != question.Message.ID || views[0].Card.SelectedValue == nil || *views[0].Card.SelectedValue != "brief" || views[0].Card.Editable || lookup.answerLookups != 1 {
		t.Fatalf("question card: %+v", views)
	}
	// Supplying both in an acknowledgement needs no database lookups.
	lookup.lookups = nil
	lookup.answerLookups = 0
	views, err = service.EnrichMessages(ctx, []loopd.Message{answered.Message, *answered.Reply})
	if err != nil {
		t.Fatal(err)
	}
	if len(lookup.lookups) != 0 || lookup.answerLookups != 0 || views[0].Card.SelectedValue == nil || views[1].Card.SelectedValue == nil {
		t.Fatal("ack projection lost selection or queried its own input")
	}
	// Cross-conversation and missing references cannot supply a card or preview.
	for _, convID := range []string{"other", "conv"} {
		copy := *answered.Reply
		copy.ConversationID = convID
		if convID == "conv" {
			copy.ReplyToID = "missing"
		}
		views, err = service.EnrichMessages(ctx, []loopd.Message{copy})
		if err != nil {
			t.Fatal(err)
		}
		if views[0].Card.Type != "content" || views[0].ReplyTo != nil || string(views[0].Content) != string(copy.Content) {
			t.Fatalf("reference scope/fallback: %+v", views[0])
		}
	}
	// Only typed user answers inherit question cards.
	ordinary := *answered.Reply
	ordinary.Purpose = "output"
	views, err = service.EnrichMessages(ctx, []loopd.Message{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Card.Type != "content" || views[0].ReplyTo == nil {
		t.Fatalf("ordinary reply inherited card: %+v", views[0])
	}
}

func TestEnrichmentBatchesReferencesWithoutFollowingChains(t *testing.T) {
	store := openServiceStore(t)
	ctx := context.Background()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []model.Message{
		{ID: "root", ConversationID: "conv", Content: textContent("root")},
		{ID: "parent", ConversationID: "conv", ReplyToID: "root", Content: textContent("parent")},
	} {
		if _, err := store.CreateMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	lookup := &countedMessageLookup{Store: store}
	service := NewMessageService(lookup, nil)
	messages := []loopd.Message{{ID: "one", ConversationID: "conv", ReplyToID: "parent", Content: textContent("one")}, {ID: "two", ConversationID: "conv", ReplyToID: "parent", Content: textContent("two")}}
	views, err := service.EnrichMessages(ctx, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(lookup.lookups) != 1 || !reflect.DeepEqual(lookup.lookups[0], []string{"parent"}) || len(views) != 2 || views[0].ReplyTo.Preview != "parent" || views[1].ReplyTo.Preview != "parent" {
		t.Fatalf("not a direct batch: %+v %+v", views, lookup.lookups)
	}
}
