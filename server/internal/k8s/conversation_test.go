package k8s

import (
	"context"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
	conversationv1 "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPollReadsDatabaseBeyondWakeSignal(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	actor := contract.ActorRef{Kind: contract.ActorKindOperator, Key: "router"}
	kube := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&conversationv1.Conversation{}).
		WithObjects(&conversationv1.Conversation{
			ObjectMeta: metav1.ObjectMeta{Name: "conv", Namespace: "test"},
			Spec: conversationv1.ConversationSpec{Participants: []conversationv1.ConversationParticipant{
				{Kind: "operator", Key: "router", EndOffset: "001"},
			}},
			Status: conversationv1.ConversationStatus{Consumers: []conversationv1.ConversationConsumer{
				{Kind: "operator", Key: "router", Committed: "001"},
			}},
		}).Build()
	c := NewConversationClient(kube, "test", 0)
	result, err := c.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]contract.Message, error) {
		if after != "001" {
			t.Fatalf("cursor = %q", after)
		}
		return []contract.Message{{
			ID: "002", ConversationID: "conv", TargetKind: actor.Kind, TargetKey: actor.Key,
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Position != "002" || len(result.Messages) != 1 {
		t.Fatalf("result = %+v", result)
	}
	value := &conversationv1.Conversation{}
	if err := kube.Get(ctx, client.ObjectKey{Name: "conv", Namespace: "test"}, value); err != nil {
		t.Fatal(err)
	}
	if value.Committed("operator", "router") != "001" {
		t.Fatalf("status = %+v", value.Status)
	}
	// A lost Poll response or restarted Operator replays the uncommitted range.
	restarted := NewConversationClient(kube, "test", 0)
	replayed, err := restarted.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]contract.Message, error) {
		if after != "001" {
			t.Fatalf("replay starts at %q", after)
		}
		return result.Messages, nil
	})
	if err != nil || replayed.Position != "002" {
		t.Fatalf("replay=%+v %v", replayed, err)
	}
	if err := restarted.Commit(ctx, "conv", contract.CommitRequest{Actor: actor, Through: "003"}); err == nil {
		t.Fatal("cannot commit beyond received position")
	}
	if err := c.Commit(ctx, "conv", contract.CommitRequest{Actor: actor, Through: result.Position}); err != nil {
		t.Fatal(err)
	}
	empty, err := c.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]contract.Message, error) {
		if after != "002" {
			t.Fatalf("cursor = %q", after)
		}
		return nil, nil
	})
	if err != nil || empty.Position != "002" || len(empty.Messages) != 0 {
		t.Fatalf("empty result = %+v, err = %v", empty, err)
	}
}

func TestSignalsPreserveIndependentRecipients(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	c := NewConversationClient(kube, "test", 0)
	a := contract.ActorRef{Kind: contract.ActorKindOperator, Key: "a"}
	b := contract.ActorRef{Kind: contract.ActorKindOperator, Key: "b"}
	for _, signal := range []struct {
		id    string
		actor contract.ActorRef
	}{
		{"001", a}, {"002", b}, {"000", a},
	} {
		if err := c.Signal(ctx, "conv", signal.id, signal.actor, 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	value := &conversationv1.Conversation{}
	key := client.ObjectKey{Name: "conv", Namespace: "test"}
	if err := kube.Get(ctx, key, value); err != nil {
		t.Fatal(err)
	}
	if value.EndOffset("operator", "a") != "001" || value.EndOffset("operator", "b") != "002" {
		t.Fatalf("signals = %+v", value.Spec)
	}
	if err := c.Signal(ctx, "conv", "003", contract.ActorRef{}, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, key, value); err != nil {
		t.Fatal(err)
	}
	if len(value.Spec.Participants) != 2 || value.EndOffset("operator", "a") != "003" ||
		value.EndOffset("operator", "b") != "003" {
		t.Fatalf("broadcast = %+v", value.Spec)
	}
}

func TestSignalsMergeDetailBindingsWithoutChangingCursors(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	a := contract.ActorRef{Kind: "operator/custom", Key: "same"}
	b := contract.ActorRef{Kind: "harness", Key: "same"}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	c := NewConversationClient(kube, "test", 0)
	for _, s := range []struct {
		actor      contract.ActorRef
		id, detail string
	}{{a, "003", "a-detail"}, {b, "004", "b-detail"}} {
		if err := c.Signal(ctx, "conv", s.id, s.actor, 1, s.detail); err != nil {
			t.Fatal(err)
		}
	}
	value := &conversationv1.Conversation{}
	key := client.ObjectKey{Name: "conv", Namespace: "test"}
	if err := kube.Get(ctx, key, value); err != nil {
		t.Fatal(err)
	}
	value.Status.Consumers = []conversationv1.ConversationConsumer{{Kind: a.Kind, Key: a.Key, Position: "003", Committed: "002"}}
	if err := kube.Status().Update(ctx, value); err != nil {
		t.Fatal(err)
	}
	// A notification without a binding and a broadcast must preserve both links.
	if err := c.Signal(ctx, "conv", "001", a, 2, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Signal(ctx, "conv", "005", contract.ActorRef{}, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, key, value); err != nil {
		t.Fatal(err)
	}
	if len(value.Spec.Participants) != 2 || value.Spec.Participants[0].ConversationID != "a-detail" || value.Spec.Participants[1].ConversationID != "b-detail" || value.Committed(a.Kind, a.Key) != "002" || value.Status.Consumers[0].Position != "003" {
		t.Fatalf("lost association/cursor: %+v", value)
	}
	// Retry the binding projection even when the wake offset does not advance.
	value.Spec.Participants[0].ConversationID = ""
	if err := kube.Update(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := c.Signal(ctx, "conv", "001", a, 2, "a-detail"); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, key, value); err != nil {
		t.Fatal(err)
	}
	if value.Spec.Participants[0].ConversationID != "a-detail" || value.EndOffset(a.Kind, a.Key) != "005" {
		t.Fatalf("repair: %+v", value.Spec)
	}
}
