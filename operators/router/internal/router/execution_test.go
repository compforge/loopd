package router

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/harness"
	loopruntime "github.com/compforge/loopd/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

// +case=`执行期间的新输入在当前批次完成后重新 plan，可直接汇总或继续分派；只发一条回答`
func TestAdditionalInputReplansAfterHarnessBatch(t *testing.T) {
	for _, furtherWork := range []bool{false, true} {
		t.Run(fmt.Sprintf("dispatch_%t", furtherWork), func(t *testing.T) {
			results := map[string]string{
				"work/0":    "The existing evidence.",
				"plan/1":    `{"kind":"summary","tasks":[]}`,
				"summarize": "Answer incorporating the addition.",
			}
			want := []string{"plan", "work/0", "plan/1"}
			if furtherWork {
				results["plan/1"] = `{"kind":"simple","tasks":["Check the new constraint using the existing evidence."]}`
				results["work/1/0"] = "New evidence."
				want = append(want, "work/1/0")
			}
			want = append(want, "summarize")
			adapter := newScriptedAdapter(`{"kind":"simple","tasks":["Initial investigation"]}`, results, 1)
			release := make(chan struct{})
			adapter.holdWork = release
			server := newLoopServer(t, "task-1")
			runtime, err := loopruntime.New(server.URL, loopruntime.Options{
				HTTPClient: server.Client(), Harnesses: map[string]harness.Adapter{"temporary": adapter},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")})
				done <- err
			}()
			select {
			case <-adapter.started:
			case <-ctx.Done():
				t.Fatal("execution Harness did not start")
			}
			server.mu.Lock()
			server.inbox = [][]loopd.Message{{{
				ID: "message-3", ConversationID: "conversation-1", TaskID: "task-2",
				Kind: loopd.RoleUser, Content: semanticModel("Please include the new constraint."),
			}}}
			listens := server.listens
			server.mu.Unlock()
			if listens != 1 {
				t.Fatalf("Listen ran before batch completion: %d", listens)
			}
			if got := adapter.effectKeys(); fmt.Sprint(got) != "[plan work/0]" {
				t.Fatalf("additional execution started early: %v", got)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if got := adapter.effectKeys(); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("effects=%v want=%v", got, want)
			}
			adapter.mu.Lock()
			prompt := adapter.prompts["plan/1"]
			summary := adapter.prompts["summarize"]
			adapter.mu.Unlock()
			if !strings.Contains(prompt, "The existing evidence.") || !strings.Contains(prompt, "new constraint") {
				t.Fatalf("replan lacks accumulated context: %s", prompt)
			}
			if furtherWork && !strings.Contains(summary, "New evidence.") {
				t.Fatal("summary lost later evidence")
			}
			server.mu.Lock()
			completed := fmt.Sprint(server.completedIDs)
			remaining := len(server.inbox)
			server.mu.Unlock()
			if completed != "[task-1 task-2]" || remaining != 0 {
				t.Fatalf("delivery closure=%s unread=%d", completed, remaining)
			}
			// A coalesced Conv wake after this run must not execute the same input again.
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")}); err != nil {
				t.Fatal(err)
			}
			if got := adapter.effectKeys(); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("duplicate execution: %v", got)
			}
		})
	}
}

func TestInputArrivingDuringSummaryReplansBeforePublishing(t *testing.T) {
	adapter := newScriptedAdapter(`{"kind":"simple","tasks":["Initial work"]}`, map[string]string{
		"work/0":      "Evidence",
		"summarize":   "Stale answer",
		"plan/1":      `{"kind":"summary","tasks":[]}`,
		"summarize/1": "Updated answer",
	}, 1)
	server := newLoopServer(t, "task-1")
	// No input at the batch boundary; one arrives before the summary boundary.
	server.inbox = [][]loopd.Message{nil, {{
		ID: "new", Kind: loopd.RoleUser, TaskID: "task-2", Content: semanticModel("A late constraint"),
	}}}
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{
		HTTPClient: server.Client(), Harnesses: map[string]harness.Adapter{"temporary": adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")}); err != nil {
		t.Fatal(err)
	}
	if got := adapter.effectKeys(); fmt.Sprint(got) != "[plan work/0 summarize plan/1 summarize/1]" {
		t.Fatalf("wrong continuation keys: %v", got)
	}
	answer, complete, failure := server.result()
	if answer != "Updated answer" || !complete || failure != nil {
		t.Fatalf("answer=%s complete=%v failure=%v", answer, complete, failure)
	}
}
