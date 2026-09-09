package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/compforge/loopd/pkg/contract"
	conversationv1 "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	k8sclient "github.com/compforge/loopd/server/internal/k8s"
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
	poll := service.NewPollService(store, k8sclient.NewConversationClient(kube, "test", 0), nil)
	chat := service.NewChatService(store, nil, poll)
	if _, err := chat.Create(ctx, "conv", "alice", contract.ActorRef{Kind: contract.ActorKindOperator, Key: "router"},
		json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"hello"}]}`)); err != nil {
		t.Fatal(err)
	}
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil, nil),
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
	var result contract.PollResult
	if first.StatusCode() != 200 {
		t.Fatalf("Poll = %d %s", first.StatusCode(), first.Body())
	}
	if err := json.Unmarshal(first.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].SourceKind != contract.ActorKindUser ||
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

// +case=`Custom Operator roles are participants with independent consumption cursors.`
func TestCustomRoleConversationConsumption(t *testing.T) {
	testActorKindConversationConsumption(t, "operator/longhorizon/manager")
}

// +case=`ActorKind 开放值贯穿持久消息、通知、Poll 与 Commit，不需要新增枚举常量。`
func TestOpenActorKindConversationConsumption(t *testing.T) {
	for _, kind := range []contract.ActorKind{contract.ActorKindOperator, contract.ActorKindHarness, "operator/planner", "operator/longhorizon/manager/delegate", "harness/local", "future-kind"} {
		t.Run(string(kind), func(t *testing.T) { testActorKindConversationConsumption(t, kind) })
	}
}

func testActorKindConversationConsumption(t *testing.T, kind contract.ActorKind) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "roles.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	scheme := kuberuntime.NewScheme()
	_ = conversationv1.AddToScheme(scheme)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	poll := service.NewPollService(store, k8sclient.NewConversationClient(kube, "test", 0), nil)
	messages := service.NewMessageService(store, nil, nil)
	// Speak persistence queues the notification; Poll service reconciles it.
	role := contract.ActorRef{Kind: kind, Key: "run-uid"}
	message, err := messages.Publish(ctx, "conv", contract.SpeakRequest{Key: "audit-report", Actor: contract.ActorRef{Kind: "operator/longhorizon/auditor", Key: "run-uid"}, Target: role})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := poll.Notify(ctx, stored); err != nil {
		t.Fatal(err)
	}
	result, err := poll.Poll(ctx, "conv", contract.PollRequest{Actor: role, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].ID != message.ID || result.Messages[0].TargetKind != kind || stored.TargetKind != kind {
		t.Fatalf("poll=%+v", result)
	}
	if err := poll.Commit(ctx, "conv", contract.CommitRequest{Actor: role, Through: result.Position}); err != nil {
		t.Fatal(err)
	}
	result, err = poll.Poll(ctx, "conv", contract.PollRequest{Actor: role})
	if err != nil || len(result.Messages) != 0 {
		t.Fatalf("committed=%+v %v", result, err)
	}
}
