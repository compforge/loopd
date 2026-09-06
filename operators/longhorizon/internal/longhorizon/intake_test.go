package longhorizon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	"github.com/compforge/loopd/pkg/contract"
)

func queueFollowup(f *fixture) contract.Message {
	f.t.Helper()
	// A different topic deliberately still belongs to the active Run. The fake
	// message IDs are ordered strings; production uses UUIDv7.
	m := contract.Message{ID: "z-followup", ConversationID: "conv", Kind: contract.ActorKindUser, Key: "alice", Purpose: "input", Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"Explain the history of Beijing."}]}`)}
	f.mu.Lock()
	f.pending = append(f.pending, m)
	f.messages[m.ID] = m
	f.mu.Unlock()
	return m
}

func TestActiveRunReceivesChangedTopicAsContinuation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.step() // Persist the first input checkpoint, before any Harness call.
	original := f.run()
	extra := queueFollowup(f)
	if _, err := f.c.Ingress(ctx, request("conv")); err != nil {
		t.Fatal(err)
	}
	var runs lh.RunList
	if err := f.c.Reader.List(ctx, &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs.Items) != 1 || runs.Items[0].UID != original.UID {
		t.Fatalf("new topic created another Run: %+v", runs.Items)
	}
	if _, err := f.c.Manager(ctx, request(original.Name)); err != nil {
		t.Fatal(err)
	}
	run := f.run()
	if run.UID != original.UID || run.Spec.Goal != original.Spec.Goal || run.Status.InputThrough != extra.ID || !strings.Contains(run.Status.Guidance, "Beijing") {
		t.Fatalf("input was not checkpointed as continuation: %+v", run)
	}
}

func TestNewRunWaitsForPriorRunFinalization(t *testing.T) {
	for _, phase := range []string{"Succeeded", "Stopped", "Failed"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.step()
			original := f.run()
			original.Status.Phase = phase
			original.Status.Summary = "Previous work has ended."
			if err := f.c.Client.Status().Update(ctx, original); err != nil {
				t.Fatal(err)
			}
			extra := queueFollowup(f)
			assertRunCount := func(want int) {
				t.Helper()
				if _, err := f.c.Ingress(ctx, request("conv")); err != nil {
					t.Fatal(err)
				}
				var runs lh.RunList
				if err := f.c.Reader.List(ctx, &runs); err != nil {
					t.Fatal(err)
				}
				if len(runs.Items) != want {
					t.Fatalf("Run count=%d, want %d", len(runs.Items), want)
				}
			}
			assertRunCount(1) // A terminal phase alone does not release intake.
			f.step()          // Persist the final message and its reference.
			if run := f.run(); run.Status.FinalMessageID == "" || run.Status.FinishedAt != nil {
				t.Fatalf("unexpected finalization checkpoint: %+v", run.Status)
			}
			assertRunCount(1)
			f.step() // Acknowledge finalization before allowing another Run.
			if f.run().Status.FinishedAt == nil {
				t.Fatal("Run finalization was not recorded")
			}
			assertRunCount(2)
			assertRunCount(2) // Repeated reconcile must not create another Run.

			var next lh.Run
			if err := f.c.Reader.Get(ctx, request(extra.ID).NamespacedName, &next); err != nil {
				t.Fatal(err)
			}
			if next.UID == original.UID || next.Spec.Conversation != original.Spec.Conversation || next.Spec.WorkspaceID != original.Spec.WorkspaceID || next.Spec.InputMessageID != extra.ID {
				t.Fatalf("unexpected successor Run: %+v", next)
			}
			if len(next.OwnerReferences) != 1 || next.OwnerReferences[0].UID != original.Spec.Conversation.UID {
				t.Fatalf("successor does not belong to the same Conv: %+v", next.OwnerReferences)
			}
			history, err := f.c.history(ctx, &next)
			if err != nil || !strings.Contains(history, original.Spec.Goal) || !strings.Contains(history, original.Status.Summary) {
				t.Fatalf("successor lost conversation context: %q, %v", history, err)
			}
			f.mu.Lock()
			committed := f.committed
			f.mu.Unlock()
			if committed != original.Spec.InputMessageID {
				t.Fatalf("successor input committed before its status checkpoint: %q", committed)
			}
		})
	}
}
