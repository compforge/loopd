package conversation

import (
	"context"
	"testing"

	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
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
	actor := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}
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
	c := NewClient(kube, "test", 0)
	result, err := c.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]loopd.Message, error) {
		if after != "001" {
			t.Fatalf("cursor = %q", after)
		}
		return []loopd.Message{{
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
	restarted := NewClient(kube, "test", 0)
	replayed, err := restarted.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]loopd.Message, error) {
		if after != "001" {
			t.Fatalf("replay starts at %q", after)
		}
		return result.Messages, nil
	})
	if err != nil || replayed.Position != "002" {
		t.Fatalf("replay=%+v %v", replayed, err)
	}
	if err := restarted.Commit(ctx, "conv", loopd.CommitRequest{Actor: actor, Through: "003"}); err == nil {
		t.Fatal("cannot commit beyond received position")
	}
	if err := c.Commit(ctx, "conv", loopd.CommitRequest{Actor: actor, Through: result.Position}); err != nil {
		t.Fatal(err)
	}
	empty, err := c.Poll(ctx, "conv", actor, "", func(_ context.Context, after string) ([]loopd.Message, error) {
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
	c := NewClient(kube, "test", 0)
	a := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"}
	b := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "b"}
	for _, signal := range []struct {
		id    string
		actor loopd.ActorRef
	}{
		{"001", a}, {"002", b}, {"000", a},
	} {
		if err := c.Signal(ctx, "conv", signal.id, signal.actor); err != nil {
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
	if err := c.Signal(ctx, "conv", "003", loopd.ActorRef{}); err != nil {
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
