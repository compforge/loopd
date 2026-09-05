package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	conversationclient "github.com/compforge/loopd/server/internal/conversation"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConversationPollHTTP(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	scheme := kuberuntime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	poll := service.NewPollService(store, conversationclient.NewClient(kube, "test", 0), nil)
	chat := service.NewChatService(store, completedChatRunner{}, nil, poll)
	if _, err := chat.Create(ctx, "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"},
		json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"hello"}]}`)); err != nil {
		t.Fatal(err)
	}
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil),
		chat, nil)
	api.Poll = poll
	engine := route.NewEngine(config.NewOptions(nil))
	api.Register(engine)
	// Ordinary history reads must not acknowledge the input.
	history := ut.PerformRequest(engine, "GET", "/v1/conversations/conv/messages", nil).Result()
	if history.StatusCode() != 200 {
		t.Fatalf("history = %s", history.Body())
	}
	first := performJSON(t, engine, "POST", "/v1/conversations/conv/poll", `{"actor":{"kind":"operator","key":"router"},"limit":10}`)
	var result loopd.PollResult
	if first.StatusCode() != 200 {
		t.Fatalf("Poll = %d %s", first.StatusCode(), first.Body())
	}
	if err := json.Unmarshal(first.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Kind != loopd.RoleUser ||
		result.Messages[0].TargetKey != "router" || result.Position != result.Messages[0].ID {
		t.Fatalf("Poll = %+v", result)
	}
	commit := performJSON(t, engine, "POST", "/v1/conversations/conv/commit", `{"actor":{"kind":"operator","key":"router"},"through":"`+result.Position+`"}`)
	if commit.StatusCode() != 204 {
		t.Fatalf("commit: %d %s", commit.StatusCode(), commit.Body())
	}
	second := performJSON(t, engine, "POST", "/v1/conversations/conv/poll", `{"actor":{"kind":"operator","key":"router"}}`)
	if second.StatusCode() != 200 {
		t.Fatalf("second Poll = %d %s", second.StatusCode(), second.Body())
	}
	if err := json.Unmarshal(second.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("second Poll delivered duplicate: %+v", result)
	}
	unknown := performJSON(t, engine, "POST", "/v1/conversations/conv/poll", `{"actor":{"kind":"operator","key":"other"}}`)
	if unknown.StatusCode() != 403 {
		t.Fatalf("nonparticipant = %d %s", unknown.StatusCode(), unknown.Body())
	}
}
