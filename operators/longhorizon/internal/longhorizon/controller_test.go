package longhorizon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/harness"
	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	lr "github.com/compforge/loopd/runtime"
	convapi "github.com/compforge/loopd/runtime/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// The HTTP fixture enforces the public message seal boundary; Kubernetes's fake
// client exercises separate status writers. Real admission and GC need Kubernetes.
type fixture struct {
	t            *testing.T
	c            *Controller
	runtime      *lr.Runtime
	server       *httptest.Server
	mu           sync.Mutex
	messages     map[string]loopd.Message
	outputs      map[string]string
	scripts      map[string][]string
	calls        map[string]int
	prompts      map[string][]string
	human        loopd.HumanResult
	humanRequest loopd.HumanRequest
	failEnd      bool
	failSeal     bool
	commitError  bool
	committed    string
	pending      []loopd.Message
	history      []loopd.Message
}

func newFixture(t *testing.T, history ...loopd.Message) *fixture {
	t.Helper()
	f := &fixture{t: t, messages: map[string]loopd.Message{}, outputs: map[string]string{}, scripts: map[string][]string{}, calls: map[string]int{}, prompts: map[string][]string{}}
	f.history = history
	for _, m := range history {
		f.messages[m.ID] = m
	}
	f.messages["input"] = loopd.Message{ID: "input", ConversationID: "conv", TaskID: "delivery", Kind: loopd.ActorKindUser, Key: "alice", Purpose: "input", Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"Create a correct artifact."}]}`)}
	f.pending = []loopd.Message{f.messages["input"]}
	scheme := runtime.NewScheme()
	if err := lh.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := convapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	conv := &convapi.Conversation{ObjectMeta: metav1.ObjectMeta{Name: "conv", Namespace: "default", UID: "conv-uid"}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&lh.Run{}, &lh.Execution{}, &lh.Audit{}).WithObjects(conv).WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetUID() == "" {
			obj.SetUID(types.UID(fmt.Sprintf("%T-%s", obj, obj.GetName())))
		}
		return c.Create(ctx, obj, opts...)
	}}).Build()
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	adapters := map[string]harness.Adapter{}
	for _, role := range []string{"manager", "executor", "auditor"} {
		adapters[role] = scriptAdapter{f, role}
	}
	r, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.server.Client(), Harnesses: adapters})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = r
	t.Cleanup(func() { _ = r.Close() })
	f.c = &Controller{Client: kube, Reader: kube, Loop: r.Loop, Config: (Config{}).defaults()}
	if _, err := f.c.Ingress(context.Background(), request("conv")); err != nil {
		t.Fatal(err)
	}
	return f
}
func request(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: name}}
}
func (f *fixture) run() *lh.Run {
	f.t.Helper()
	var r lh.Run
	if err := f.c.Reader.Get(context.Background(), request("input").NamespacedName, &r); err != nil {
		f.t.Fatal(err)
	}
	return &r
}
func (f *fixture) step() {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.c.Manager(ctx, request("input")); err != nil {
		f.t.Fatal(err)
	}
	var es lh.ExecutionList
	var as lh.AuditList
	if err := f.c.Client.List(ctx, &es); err != nil {
		f.t.Fatal(err)
	}
	if err := f.c.Client.List(ctx, &as); err != nil {
		f.t.Fatal(err)
	}
	for _, e := range es.Items {
		if _, err := f.c.Executor(ctx, request(e.Name)); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, a := range as.Items {
		if _, err := f.c.Auditor(ctx, request(a.Name)); err != nil {
			f.t.Fatal(err)
		}
	}
}
func (f *fixture) until(predicate func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out: %+v", f.run().Status)
		}
		f.step()
		time.Sleep(time.Millisecond)
	}
}
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(r.URL.Path, "/poll"):
		var in loopd.PollRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		after := in.After
		if after == "" {
			after = f.committed
		}
		var messages []loopd.Message
		for _, m := range f.pending {
			if m.ID > after {
				messages = append(messages, m)
				if len(messages) >= in.Limit {
					break
				}
			}
		}
		position := after
		if len(messages) > 0 {
			position = messages[len(messages)-1].ID
		}
		write(loopd.PollResult{Messages: messages, Position: position, Committed: f.committed})
	case strings.HasSuffix(r.URL.Path, "/commit"):
		if f.commitError {
			w.WriteHeader(503)
			return
		}
		var in loopd.CommitRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Through > f.committed {
			f.committed = in.Through
		}
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/actors"):
		write(loopd.Conversation{ID: "workspace"})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages"):
		convID := strings.Split(r.URL.Path, "/")[3]
		after := r.URL.Query().Get("after")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var messages []loopd.Message
		for _, m := range f.messages {
			include := m.ConversationID == convID
			if convID == "conv" {
				for _, prior := range f.history {
					if prior.ID == m.ID {
						include = true
					}
				}
			}
			if include && m.ID > after {
				messages = append(messages, m)
			}
		}
		sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
		if len(messages) > limit {
			messages = messages[:limit]
		}
		write(map[string]any{"data": messages})
	case strings.HasSuffix(r.URL.Path, "/speak"):
		var in loopd.SpeakRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.t.Error(err)
		}
		id := f.outputs[in.Key]
		if id == "" {
			id = fmt.Sprintf("output-%d", len(f.outputs))
			f.outputs[in.Key] = id
			conv := strings.Split(r.URL.Path, "/")[3]
			content := in.Content
			if len(content) == 0 {
				content = json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
			}
			if !in.Stream {
				content = endContent(content)
			}
			f.messages[id] = loopd.Message{ID: id, ConversationID: conv, Kind: in.Actor.Kind, Key: in.Actor.Key, TargetKind: in.Target.Kind, TargetKey: in.Target.Key, ReplyToID: in.ReplyToID, Purpose: "output", Revision: 1, Content: content}
		}
		write(f.messages[id])
	case strings.HasPrefix(r.URL.Path, "/v1/messages/") && strings.HasSuffix(r.URL.Path, "/events"):
		id := strings.Split(r.URL.Path, "/")[3]
		m, ok := f.messages[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Event ui.Event `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.t.Error(err)
		}
		if in.Event.Op == ui.OpEnd {
			if f.failEnd && strings.Contains(string(m.Content), `"id":"report"`) {
				w.WriteHeader(503)
				return
			}
			m.Content = endContent(m.Content)
			m.Revision = in.Event.Seq
			f.messages[id] = m
			write(map[string]string{"id": "end"})
			return
		}
		if f.failSeal && in.Event.Block["id"] == "report" {
			w.WriteHeader(503)
			return
		}
		if in.Event.Seq != m.Revision+1 {
			f.t.Errorf("noncontiguous sequence: %d after %d", in.Event.Seq, m.Revision)
			w.WriteHeader(409)
			return
		}
		var model map[string]any
		_ = json.Unmarshal(m.Content, &model)
		updated, err := ui.Apply(model, in.Event)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		m.Content, _ = json.Marshal(updated)
		m.Revision = in.Event.Seq
		f.messages[id] = m
		write(map[string]string{"id": fmt.Sprint(in.Event.Seq)})
	case r.URL.Path == "/v1/conversations/conv/human":
		if err := json.NewDecoder(r.Body).Decode(&f.humanRequest); err != nil {
			f.t.Error(err)
		}
		f.human.Message.ID = "question"
		f.human.Status = loopd.HumanPending
		write(f.human)
	case r.URL.Path == "/v1/human/question":
		write(f.human)
	default:
		f.t.Errorf("unexpected API %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}

}

type scriptAdapter struct {
	f    *fixture
	role string
}

func (a scriptAdapter) Prompt(_ context.Context, r harness.Request) (harness.Call, error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	i := a.f.calls[a.role]
	a.f.calls[a.role]++
	a.f.prompts[a.role] = append(a.f.prompts[a.role], r.Prompt)
	if i >= len(a.f.scripts[a.role]) {
		return nil, fmt.Errorf("unexpected %s call %d", a.role, i)
	}
	events := make(chan harness.Event, 1)
	data, err := (ui.Event{Op: ui.OpSet, Block: map[string]any{"id": "answer", "type": "text", "content": a.f.scripts[a.role][i]}}).Marshal()
	if err != nil {
		return nil, err
	}
	events <- harness.Event{Data: data}
	close(events)
	return scriptCall{r.CallID, a.f.scripts[a.role][i], events}, nil
}

type scriptCall struct {
	id, text string
	events   <-chan harness.Event
}

func (c scriptCall) ID() string                   { return c.id }
func (c scriptCall) Events() <-chan harness.Event { return c.events }
func (c scriptCall) Wait(context.Context) (harness.Result, error) {
	return harness.Result{Text: c.text}, nil
}

// @case An incomplete audit drives another execution; only current independent
// evidence permits done. Consumed CRDs disappear while role reports remain.
func TestThreeRoleLoop(t *testing.T) {
	f := newFixture(t)
	f.scripts["manager"] = []string{`{"next":"cli","summary":"start","plan":"create artifact"}`, `{"next":"cli","summary":"repair","plan":"fix missing requirement"}`, `{"next":"done","summary":"Artifact independently verified."}`}
	f.scripts["executor"] = []string{"Created artifact.", "Fixed artifact."}
	f.scripts["auditor"] = []string{`{"complete":false,"integrity":"clean","task_state":"partial","evidence":"artifact misses requirement","feedback":"fix it"}`, `{"complete":true,"integrity":"clean","task_state":"complete","evidence":"artifact inspected against goal","feedback":"verified"}`}
	f.until(func() bool { return f.run().Status.FinishedAt != nil })
	run := f.run()
	if run.Status.Phase != "Succeeded" || len(run.Status.Rounds) != 3 {
		t.Fatalf("status=%+v", run.Status)
	}
	if len(run.OwnerReferences) != 1 || run.OwnerReferences[0].UID != "conv-uid" {
		t.Fatalf("owner=%v", run.OwnerReferences)
	}
	var es lh.ExecutionList
	var as lh.AuditList
	_ = f.c.Client.List(context.Background(), &es)
	_ = f.c.Client.List(context.Background(), &as)
	if len(es.Items)+len(as.Items) != 0 {
		t.Fatal("consumed children retained")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls["manager"] != 3 || f.calls["executor"] != 2 || f.calls["auditor"] != 2 {
		t.Fatalf("calls=%v", f.calls)
	}
	for key, id := range f.outputs {
		m := f.messages[id]
		if m.Key != string(run.UID) || !strings.HasPrefix(string(m.Kind), "operator/longhorizon/") {
			t.Fatalf("author=%+v", m)
		}
		if strings.HasSuffix(key, "/report") || strings.HasSuffix(key, "/final") {
			if _, err := reportFrom(m); err != nil {
				t.Fatalf("missing report %s: %v", key, err)
			}
		}
	}
	if !strings.Contains(f.prompts["manager"][1], "partial") || !strings.Contains(f.prompts["auditor"][0], run.Spec.Goal) {
		t.Fatal("verified facts/original goal missing")
	}
}

// @case A sealed report survives a lost CRD status write and a fresh runtime;
// downstream progress waits when the server cannot acknowledge durable sealing.
func TestReportCheckpointAndPersistFailure(t *testing.T) {
	f := newFixture(t)
	f.scripts["manager"] = []string{`{"next":"cli","summary":"start","plan":"create artifact"}`}
	f.scripts["executor"] = []string{"Finished before crash"}
	f.until(func() bool { return f.run().Status.Phase == "Executing" })
	run := f.run()
	e := lh.Execution{}
	if err := f.c.Reader.Get(context.Background(), request(run.Status.Execution.Name).NamespacedName, &e); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.failSeal = true
	f.mu.Unlock()
	// Harness may have finished, but a failed DB acknowledgement cannot advance the child.
	failed := false
	for i := 0; i < 100; i++ {
		_, err := f.c.Executor(context.Background(), request(e.Name))
		if err != nil {
			failed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !failed {
		t.Fatal("expected report persistence failure")
	}
	_ = f.c.Reader.Get(context.Background(), client.ObjectKeyFromObject(&e), &e)
	if e.Status.MessageID != "" {
		t.Fatal("unpersisted report consumed")
	}
	f.mu.Lock()
	f.failSeal = false
	f.mu.Unlock()
	if _, err := f.c.Executor(context.Background(), request(e.Name)); err != nil {
		t.Fatal(err)
	}
	_ = f.c.Reader.Get(context.Background(), client.ObjectKeyFromObject(&e), &e)
	saved := e.Status.MessageID
	// Simulate a lost status write after the report was persisted, then lose runtime Calls.
	e.Status = lh.WorkStatus{}
	if err := f.c.Client.Status().Update(context.Background(), &e); err != nil {
		t.Fatal(err)
	}
	fresh, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	f.c.Loop = fresh.Loop
	if _, err := f.c.Executor(context.Background(), request(e.Name)); err != nil {
		t.Fatal(err)
	}
	_ = f.c.Reader.Get(context.Background(), client.ObjectKeyFromObject(&e), &e)
	if saved == "" || e.Status.MessageID != saved || e.Status.Phase != "Completed" {
		t.Fatalf("status=%+v", e.Status)
	}
}
func TestHumanFallbackAndBudget(t *testing.T) {
	for _, outcome := range []struct {
		name   string
		status loopd.HumanStatus
		value  string
		budget int32
		phase  string
	}{{"timeout", loopd.HumanTimeout, "", 25, "Stopped"}, {"dismiss", loopd.HumanDismissed, "", 25, "Stopped"}, {"decline", loopd.HumanSuccess, "declined", 25, "Stopped"}, {"accept", loopd.HumanSuccess, "accepted", 50, "Receiving"}} {
		t.Run(outcome.name, func(t *testing.T) {
			f := newFixture(t)
			r := f.run()
			r.Status = lh.RunStatus{Phase: "WaitingForHuman", HumanReason: "budget", Round: 26, Budget: 25}
			if err := f.c.Client.Status().Update(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			if f.humanRequest.Timeout <= 0 || f.humanRequest.Actor.Kind != ActorManager {
				t.Errorf("request=%+v", f.humanRequest)
			}
			f.human.Status = outcome.status
			f.human.Value = outcome.value
			f.mu.Unlock()
			if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
				t.Fatal(err)
			}
			r = f.run()
			if r.Status.Phase != outcome.phase || r.Status.Budget != outcome.budget {
				t.Fatalf("status=%+v", r.Status)
			}
		})
	}
}
func TestDeletedOrReplacedConversationDoesNotDispatch(t *testing.T) {
	for _, mode := range []string{"deleted", "deleting", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			var task convapi.Conversation
			if err := f.c.Reader.Get(context.Background(), request("conv").NamespacedName, &task); err != nil {
				t.Fatal(err)
			}
			if mode == "deleting" {
				task.Finalizers = []string{"test/hold"}
				if err := f.c.Client.Update(context.Background(), &task); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.c.Client.Delete(context.Background(), &task); err != nil {
				t.Fatal(err)
			}
			if mode == "replaced" {
				task.ResourceVersion = ""
				task.UID = "replacement"
				if err := f.c.Client.Create(context.Background(), &task); err != nil {
					t.Fatal(err)
				}
			}
			f.step()
			if f.run().Status.Phase != "" {
				t.Fatal("retired Conv dispatched")
			}
		})
	}
}
func TestCompletionRequiresCurrentCleanAudit(t *testing.T) {
	r := &lh.Run{Status: lh.RunStatus{ContractVersion: 2, LastAudit: &lh.AuditEvidence{Execution: lh.Reference{UID: "execution"}, MessageID: "audit", ContractVersion: 1, Verdict: lh.Verdict{Complete: true, Integrity: "clean", Evidence: "file"}}}}
	if canFinish(r) {
		t.Fatal("stale contract accepted")
	}
	r.Status.LastAudit.ContractVersion = 2
	if !canFinish(r) {
		t.Fatal("current audit rejected")
	}
	r.Status.LastAudit.Verdict.Integrity = "violated"
	if canFinish(r) {
		t.Fatal("integrity violation accepted")
	}
	for i := int32(1); i <= 60; i++ {
		r.Status.Round = i
		appendRound(r, "cli", "summary", nil)
	}
	if len(r.Status.Rounds) != 50 || r.Status.Rounds[0].Index != 11 {
		t.Fatal("round history unbounded")
	}
}

func TestNewRunReferencesOnlyImmutablePriorContext(t *testing.T) {
	prior := loopd.Message{ID: "earlier", ConversationID: "previous", Kind: loopd.ActorKindUser, Key: "alice", Purpose: "input", Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"Artifact must include a license."}]}`)}
	active := prior
	active.ID = "still-running"
	active.Kind = loopd.ActorKindOperator
	active.Purpose = "response"
	f := newFixture(t, prior, active)
	run := f.run()
	if len(run.Spec.ContextMessages) != 1 || run.Spec.ContextMessages[0].MessageID != prior.ID {
		t.Fatalf("references=%+v", run.Spec.ContextMessages)
	}
	history, err := f.c.history(context.Background(), run)
	if err != nil || !strings.Contains(history, "license") || !strings.Contains(history, "user/alice") {
		t.Fatalf("context=%s %v", history, err)
	}
}

func TestHumanGuidanceAccumulatesAndInvalidatesAudit(t *testing.T) {
	f := newFixture(t)
	r := f.run()
	r.Status = lh.RunStatus{Phase: "WaitingForHuman", HumanReason: "ask", HumanMessageID: "question", Round: 2, Guidance: "Preserve the license.", LastAudit: &lh.AuditEvidence{MessageID: "old-audit"}}
	if err := f.c.Client.Status().Update(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.human = loopd.HumanResult{Status: loopd.HumanSuccess, Value: "Also add tests."}
	f.mu.Unlock()
	if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
		t.Fatal(err)
	}
	r = f.run()
	if r.Status.Guidance != "Preserve the license.\nAlso add tests." || r.Status.LastAudit != nil || r.Status.Round != 3 {
		t.Fatalf("status=%+v", r.Status)
	}
}

func TestHistoricalHumanReplyUsesExplicitQuestion(t *testing.T) {
	question := loopd.Message{ID: "a-question", ConversationID: "previous", Kind: ActorManager, Purpose: "human_request", Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"human","type":"ask","prompt":"Which language?","choices":[{"value":"a","label":"Go"}]}]}`)}
	reply := loopd.Message{ID: "a-reply", ConversationID: "previous", Kind: loopd.ActorKindUser, Purpose: "human_reply", ReplyToID: question.ID, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"human","type":"human_reply","outcome":"success","value":"a"}]}`)}
	f := newFixture(t, question, reply)
	history, err := f.c.history(context.Background(), f.run())
	if err != nil || !strings.Contains(history, "Which language?") || !strings.Contains(history, "a: Go") || !strings.Contains(history, "success a") {
		t.Fatalf("history=%s %v", history, err)
	}
}

// @case New user input is checkpointed at a round boundary before Commit, and
// invalidates old audit evidence. Ending one message cannot finish a Run.
func TestContinuousInputCheckpointAndMessageEndIndependence(t *testing.T) {
	f := newFixture(t)
	f.step() // persist initial Run status
	if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
		t.Fatal(err)
	}
	run := f.run()
	run.Status.Phase = "Executing"
	run.Status.ContractVersion = 3
	run.Status.LastAudit = &lh.AuditEvidence{MessageID: "prior"}
	if err := f.c.Client.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	extra := loopd.Message{ID: "more-input", ConversationID: "conv", Kind: loopd.ActorKindUser, Key: "alice", Purpose: "input", Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"Also include a license."}]}`)}
	f.mu.Lock()
	f.pending = append(f.pending, extra)
	f.messages[extra.ID] = extra
	f.mu.Unlock()
	observation, err := f.c.Loop.Conv.Speak(context.Background(), "workspace", loopd.SpeakRequest{Key: "independent-observation", Actor: actor(run, ActorManager), Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.End(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.run().Status.Phase != "Executing" || f.run().Status.InputThrough != "input" {
		t.Fatal("message End changed business work")
	}
	// The next safe round boundary receives the additional input.
	run = f.run()
	run.Status.Phase = "Receiving"
	if err := f.c.Client.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
		t.Fatal(err)
	}
	run = f.run()
	if run.Status.InputThrough != extra.ID || !strings.Contains(run.Status.Guidance, "license") || run.Status.LastAudit != nil || run.Status.ContractVersion != 4 {
		t.Fatalf("checkpoint=%+v", run.Status)
	}
	f.mu.Lock()
	f.commitError = true
	committed := f.committed
	f.mu.Unlock()
	if committed != "input" {
		t.Fatal("committed before checkpoint")
	}
	if _, err := f.c.Manager(context.Background(), request("input")); err == nil {
		t.Fatal("expected commit failure")
	}
	f.mu.Lock()
	starts := f.calls["manager"]
	f.mu.Unlock()
	if starts != 0 {
		t.Fatal("new work started before checkpoint commit retry")
	}
	// Retry with a fresh process and no adapters: the checkpoint must commit first.
	fresh, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	f.c.Loop = fresh.Loop
	f.mu.Lock()
	f.commitError = false
	f.mu.Unlock()
	_, _ = f.c.Manager(context.Background(), request("input")) // missing adapter is expected after Commit
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.committed != extra.ID {
		t.Fatal("restart lost persisted input checkpoint")
	}
}

// @case Run expiry and retention are Operator-owned; removing domain resources
// preserves the Conv, committed input position, and visible final message.
func TestRunDeadlineAndRetention(t *testing.T) {
	f := newFixture(t)
	run := f.run()
	run.Spec.DeadlineAt = metav1.NewTime(time.Now().Add(-time.Minute))
	if err := f.c.Client.Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	f.until(func() bool { return f.run().Status.FinishedAt != nil })
	run = f.run()
	if run.Status.Phase != "Failed" || run.Status.FinalMessageID == "" {
		t.Fatalf("status=%+v", run.Status)
	}
	finalID := run.Status.FinalMessageID
	run.Status.FinishedAt = &metav1.Time{Time: time.Now().Add(-48 * time.Hour)}
	if err := f.c.Client.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Manager(context.Background(), request("input")); err != nil {
		t.Fatal(err)
	}
	var absent lh.Run
	if err := f.c.Reader.Get(context.Background(), request("input").NamespacedName, &absent); err == nil {
		t.Fatal("expired Run retained")
	}
	var conv convapi.Conversation
	if err := f.c.Reader.Get(context.Background(), request("conv").NamespacedName, &conv); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	_, exists := f.messages[finalID]
	committed := f.committed
	f.mu.Unlock()
	if !exists || committed != "input" {
		t.Fatal("cleanup lost durable message or cursor")
	}
	if _, err := f.c.Ingress(context.Background(), request("conv")); err != nil {
		t.Fatal(err)
	}
	var runs lh.RunList
	_ = f.c.Client.List(context.Background(), &runs)
	if len(runs.Items) != 0 {
		t.Fatal("cleanup replayed committed input")
	}
}

func endContent(content json.RawMessage) json.RawMessage {
	var value map[string]any
	_ = json.Unmarshal(content, &value)
	if value == nil {
		value = map[string]any{"version": "1.0", "biz": "chat", "blocks": []any{}}
	}
	meta, _ := value["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		value["meta"] = meta
	}
	meta["output"] = map[string]any{"ended": true}
	result, _ := json.Marshal(value)
	return result
}

// +case=`End failure leaves a durable report; restart retries End without rerunning the Harness.`
func TestReportEndRecovery(t *testing.T) {
	f := newFixture(t)
	f.scripts["executor"] = []string{"Persisted report"}
	f.failEnd = true
	run := f.run()
	var id string
	var err error
	for i := 0; i < 100; i++ {
		_, id, _, _, err = f.c.invoke(context.Background(), run, 1, ActorExecutor, "executor", "execute", time.Minute)
		if err != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err == nil || id == "" {
		t.Fatal("expected report End failure")
	}
	if _, err := f.c.readReport(context.Background(), "workspace", id); err == nil {
		t.Fatal("open report consumed")
	}
	f.mu.Lock()
	f.failEnd = false
	f.mu.Unlock()
	fresh, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	f.c.Loop = fresh.Loop
	result, restored, _, done, err := f.c.invoke(context.Background(), run, 1, ActorExecutor, "executor", "execute", time.Minute)
	if err != nil || !done || restored != id || result.Text != "Persisted report" {
		t.Fatalf("result=%+v done=%v id=%s err=%v", result, done, restored, err)
	}
	if _, err := f.c.readReport(context.Background(), "workspace", id); err != nil {
		t.Fatal(err)
	}
}

// +case=`Read selects the stable pre-input tail across pages without consuming later messages.`
func TestHistoryPaginationBoundaries(t *testing.T) {
	var history []loopd.Message
	for i := 0; i < 225; i++ {
		history = append(history, loopd.Message{ID: fmt.Sprintf("a%03d", i), ConversationID: "conv", Kind: loopd.ActorKindUser, Key: "alice", Purpose: "input"})
	}
	history = append(history, loopd.Message{ID: "z-later", ConversationID: "conv", Kind: loopd.ActorKindUser, Key: "alice", Purpose: "input"})
	f := newFixture(t, history...)
	refs := f.run().Spec.ContextMessages
	if len(refs) != 20 || refs[0].MessageID != "a205" || refs[19].MessageID != "a224" {
		t.Fatalf("refs=%+v", refs)
	}
}
