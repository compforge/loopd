package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	loopruntime "github.com/compforge/loopd/runtime"
	"github.com/compforge/loopd/server/testutil"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestRouterReportsAndCommitsReturnedError(t *testing.T) {
	server := newLoopServer(t, "")
	adapter := newScriptedAdapter(`{"kind":"simple","tasks":["Work"]}`, nil, 0)
	client := testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil)
	transport := client.Transport
	client.Transport = observationTransport{base: transport}
	rt, err := loopruntime.New(server.URL, loopruntime.Options{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	reconciler, err := New(rt.Loop, Config{HarnessTarget: "temporary"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler.reader = routerReader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")})
	var runtimeErr *loopruntime.Error
	if !errors.As(err, &runtimeErr) || runtimeErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected observation failure, got %v", err)
	}
	answer, committed, failure := server.result()
	if answer != "" || !committed || failure == nil {
		t.Fatalf("error must be reported and committed: answer=%q committed=%v failure=%v", answer, committed, failure)
	}
}

type observationTransport struct{ base http.RoundTripper }

func (t observationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/harness/runs/") {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return t.base.RoundTrip(r)
}

// +case=`执行期间的新输入在当前批次完成后重新 plan；可先发阶段结果，再汇总或继续分派`
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
				HTTPClient: testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
			if reconciler != nil {
				reconciler.reader = routerReader(t)
			}
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
			server.inbox = [][]contract.Message{{{
				ID: "message-3", ConversationID: "conversation-1", TaskID: "task-2",
				Kind: contract.ActorKindUser, Content: semanticModel("Please include the new constraint."),
			}}}
			polls := server.polls
			server.mu.Unlock()
			if polls != 1 {
				t.Fatalf("Poll ran before batch completion: %d", polls)
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
			if completed != "[message-3]" || remaining != 0 {
				t.Fatalf("committed prefix=%s unread=%d", completed, remaining)
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

func TestInputArrivingDuringSummaryDoesNotStarveCurrentAnswer(t *testing.T) {
	adapter := newScriptedAdapter(`{"kind":"simple","tasks":["Initial work"]}`, map[string]string{
		"work/0":      "Evidence",
		"summarize":   "Stale answer",
		"plan/1":      `{"kind":"summary","tasks":[]}`,
		"summarize/1": "Updated answer",
	}, 1)
	server := newLoopServer(t, "task-1")
	// No input at the batch boundary; one arrives before the summary boundary.
	server.inbox = [][]contract.Message{nil, {{
		ID: "new", Kind: contract.ActorKindUser, TaskID: "task-2", Content: semanticModel("A late constraint"),
	}}}
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{
		HTTPClient: testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if reconciler != nil {
		reconciler.reader = routerReader(t)
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")}); err != nil {
		t.Fatal(err)
	}
	if got := adapter.effectKeys(); fmt.Sprint(got) != "[plan work/0 summarize]" {
		t.Fatalf("wrong continuation keys: %v", got)
	}
	answer, complete, failure := server.result()
	if answer != "Stale answer" || !complete || failure != nil {
		t.Fatalf("answer=%s complete=%v failure=%v", answer, complete, failure)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.inbox) != 1 {
		t.Fatalf("later input must remain for the next reconcile: %+v", server.inbox)
	}
}

func TestRouterConsumesMessageWithoutUIDelivery(t *testing.T) {
	server := newLoopServer(t, "")
	adapter := newScriptedAdapter(`{"kind":"simple","tasks":["Work"]}`, map[string]string{"work/0": "Evidence", "summarize": "Result"}, 1)
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{
		HTTPClient: testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if reconciler != nil {
		reconciler.reader = routerReader(t)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: objectKey("conversation-1")}); err != nil {
		t.Fatal(err)
	}
	answer, completed, failure := server.result()
	if answer != "Result" || !completed || failure != nil {
		t.Fatalf("message work must not require a UI delivery: answer=%q completed=%v failure=%v", answer, completed, failure)
	}
}

// +case=`Router owns capacity-error policy: report a failed message in one Speak, then Commit; a failed report leaves input uncommitted.`
func TestRouterReportsCapacityError(t *testing.T) {
	for _, failReport := range []bool{false, true} {
		t.Run(fmt.Sprintf("report_failure_%t", failReport), func(t *testing.T) {
			server := newLoopServer(t, "")
			client := server.Client()
			client.Transport = capacityTransport{base: client.Transport, failReport: failReport, t: t}
			rt, err := loopruntime.New(server.URL, loopruntime.Options{HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			reconciler, err := New(rt.Loop, Config{HarnessTarget: "temporary"})
			if err != nil {
				t.Fatal(err)
			}
			reconciler.reader = routerReader(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: objectKey("conversation-1")})
			if !loopruntime.IsHarnessCapacityExceeded(err) {
				t.Fatalf("expected capacity error, got %v", err)
			}
			answer, committed, failure := server.result()
			if failReport {
				if committed || failure != nil {
					t.Fatalf("failed report committed=%v failure=%v", committed, failure)
				}
			} else if answer != "" || !committed || failure == nil || failure.Code != "router_failed" {
				t.Fatalf("report: answer=%q committed=%v failure=%v", answer, committed, failure)
			}
		})
	}
}

type capacityTransport struct {
	base       http.RoundTripper
	failReport bool
	t          *testing.T
}

func (transport capacityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := 0, ""
	if r.Method == http.MethodPost && r.URL.Path == "/v1/harness/runs" {
		status, body = http.StatusTooManyRequests, `{"error":{"type":"harness_capacity_exceeded","message":"Harness execution capacity exhausted"}}`
	} else if transport.failReport && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
		status, body = http.StatusBadRequest, `{"error":{"type":"invalid","message":"cannot publish"}}`
	} else if strings.HasSuffix(r.URL.Path, "/events") {
		transport.t.Error("complete error report should not require Emit or End")
	}
	if status != 0 {
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	return transport.base.RoundTrip(r)
}
