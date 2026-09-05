package interaction

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	rt "github.com/compforge/loopd/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

const conversationID = "conv"

func userMessage(id, taskID, text string) loopd.Message {
	content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat",
		"blocks": []map[string]any{{"id": "text", "type": "text", "content": text}}})
	return loopd.Message{ID: id, ConversationID: conversationID, Kind: loopd.RoleUser,
		Key: "user", TargetKind: actor.Kind, TargetKey: actor.Key, Purpose: "input",
		TaskID: taskID, Content: content}
}

type fixture struct {
	t                 *testing.T
	server            *httptest.Server
	mu                sync.Mutex
	messages          []loopd.Message
	questions         map[string]loopd.HumanRequest
	results           map[string]loopd.HumanResult
	answers           map[string]loopd.SpeakRequest
	committed         string
	failPath          string
	loseSpeakResponse bool
}

func newFixture(t *testing.T, messages ...loopd.Message) *fixture {
	f := &fixture{t: t, messages: messages, questions: map[string]loopd.HumanRequest{},
		results: map[string]loopd.HumanResult{}, answers: map[string]loopd.SpeakRequest{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == f.failPath {
		f.failPath = ""
		http.Error(w, "injected failure", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations/conv/poll":
		var request loopd.PollRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
			return
		}
		if request.Actor != actor || request.Limit != 1 || request.After != "" {
			f.t.Errorf("unexpected Poll: %+v", request)
		}
		result := loopd.PollResult{Committed: f.committed, Position: f.committed}
		for _, message := range f.messages {
			if message.ID > f.committed {
				result.Messages = []loopd.Message{message}
				result.Position = message.ID
				break
			}
		}
		_ = json.NewEncoder(w).Encode(result)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations/conv/commit":
		var request loopd.CommitRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
			return
		}
		if request.Actor != actor || request.Through < f.committed {
			f.t.Errorf("invalid Commit: %+v", request)
		}
		f.committed = request.Through
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations/conv/human":
		var request loopd.HumanRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
			return
		}
		if err := request.Validate(); err != nil {
			f.t.Error(err)
		}
		if request.Timeout != 10*time.Second || request.ConversationID != conversationID ||
			request.Actor != actor || request.Target != (loopd.ActorRef{Kind: loopd.RoleUser, Key: "user"}) ||
			!strings.HasPrefix(request.EffectKey, request.ReplyToID+"/") {
			f.t.Errorf("invalid question identity or timeout: %+v", request)
		}
		if prior, ok := f.questions[request.EffectKey]; ok && !reflect.DeepEqual(prior, request) {
			f.t.Error("retry changed immutable question")
		}
		if request.Type == "confirm" && f.results[request.ReplyToID+"/answer-style"].Status != loopd.HumanSuccess {
			f.t.Error("Confirm created before Ask was answered")
		}
		f.questions[request.EffectKey] = request
		result, exists := f.results[request.EffectKey]
		if !exists {
			result = loopd.HumanResult{Status: loopd.HumanPending, Deadline: time.Now().Add(request.Timeout)}
		}
		result.Message.ID = request.EffectKey
		f.results[request.EffectKey] = result
		_ = json.NewEncoder(w).Encode(result)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/human/"):
		_ = json.NewEncoder(w).Encode(f.results[strings.TrimPrefix(r.URL.Path, "/v1/human/")])
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations/conv/speak":
		var request loopd.SpeakRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
			return
		}
		if request.Actor != actor || request.Target != (loopd.ActorRef{Kind: loopd.RoleUser, Key: "user"}) ||
			request.Key != request.ReplyToID+"/summary" || request.Stream {
			f.t.Errorf("invalid summary identity: %+v", request)
		}
		if prior, exists := f.answers[request.Key]; exists && !reflect.DeepEqual(prior, request) {
			f.t.Error("retry changed summary")
		}
		f.answers[request.Key] = request
		if f.loseSpeakResponse {
			f.loseSpeakResponse = false
			http.Error(w, "response lost after persistence", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(loopd.Message{ID: request.Key, Content: request.Content})
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fixture) reconcile() (ctrl.Result, error) {
	f.t.Helper()
	// A fresh Runtime/Reconciler every time exercises restart recovery.
	runtime, err := rt.New(f.server.URL, rt.Options{})
	if err != nil {
		f.t.Fatal(err)
	}
	defer runtime.Close()
	return New(runtime.Loop).Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: conversationID, Namespace: "demo"}})
}

func (f *fixture) step(wantDelay time.Duration) {
	f.t.Helper()
	result, err := f.reconcile()
	if err != nil || result.RequeueAfter != wantDelay {
		f.t.Fatalf("result=%+v err=%v; want delay=%s", result, err, wantDelay)
	}
}

func (f *fixture) resolve(key string, result loopd.HumanResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prior := f.results[key]
	result.Message = prior.Message
	result.Deadline = prior.Deadline
	f.results[key] = result
}

// +case=`Ask → Confirm 串行；取消/超时收口；重启复用消息与期限；仅汇总后 Commit`
func TestSequentialInteraction(t *testing.T) {
	for _, test := range []struct {
		name         string
		ask, confirm loopd.HumanResult
		want         string
	}{
		{"confirmed", loopd.HumanResult{Status: loopd.HumanSuccess, Value: "steps"}, loopd.HumanResult{Status: loopd.HumanSuccess, Value: "accepted"}, "你的确认：已确认"},
		{"ask canceled", loopd.HumanResult{Status: loopd.HumanDismissed}, loopd.HumanResult{}, "你的选择：已取消"},
		{"ask timeout", loopd.HumanResult{Status: loopd.HumanTimeout}, loopd.HumanResult{}, "你的选择：已超时"},
		{"ask failure", loopd.HumanResult{Status: loopd.HumanFailure, Reason: "unavailable"}, loopd.HumanResult{}, "交互失败：unavailable"},
		{"confirm canceled", loopd.HumanResult{Status: loopd.HumanSuccess, Value: "brief"}, loopd.HumanResult{Status: loopd.HumanDismissed}, "你的确认：已取消"},
		{"confirm declined", loopd.HumanResult{Status: loopd.HumanSuccess, Value: "examples"}, loopd.HumanResult{Status: loopd.HumanSuccess, Value: "declined"}, "你的确认：已取消"},
		{"confirm timeout", loopd.HumanResult{Status: loopd.HumanSuccess, Value: "brief"}, loopd.HumanResult{Status: loopd.HumanTimeout}, "你的确认：已超时"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, userMessage("01", "delivery", "如何学习 Go？"))
			f.step(time.Second)
			f.mu.Lock()
			deadline := f.results["01/answer-style"].Deadline
			f.mu.Unlock()
			f.step(time.Second)
			f.mu.Lock()
			if len(f.questions) != 1 || len(f.answers) != 0 || f.committed != "" ||
				!f.results["01/answer-style"].Deadline.Equal(deadline) {
				t.Error("pending interaction advanced or reset deadline")
			}
			f.mu.Unlock()
			f.resolve("01/answer-style", test.ask)
			if test.ask.Status == loopd.HumanSuccess {
				f.step(time.Second)
				f.mu.Lock()
				if len(f.questions) != 2 || len(f.answers) != 0 || f.committed != "" {
					t.Error("did not wait for Confirm")
				}
				f.mu.Unlock()
				f.resolve("01/confirm-style", test.confirm)
			}
			f.step(time.Millisecond)
			f.step(0)
			f.mu.Lock()
			defer f.mu.Unlock()
			answer := string(f.answers["01/summary"].Content)
			if f.committed != "01" || len(f.answers) != 1 ||
				!strings.Contains(answer, test.want) || !strings.Contains(answer, "如何学习 Go？") {
				t.Fatalf("committed=%s answer=%s", f.committed, answer)
			}
			if test.ask.Status != loopd.HumanSuccess && len(f.questions) != 1 {
				t.Error("unsuccessful Ask still created Confirm")
			}
		})
	}
}

// +case=`等待期间允许追加普通发言；卡片回复不触发新 Ask；无 TaskID 也能发言与消费`
func TestContinuousInputAndTypedReplies(t *testing.T) {
	f := newFixture(t, userMessage("01", "", "第一个问题"))
	f.step(time.Second)
	f.mu.Lock()
	f.messages = append(f.messages, userMessage("02", "", "确认"),
		loopd.Message{ID: "03", Kind: loopd.RoleUser, Key: "user", Purpose: "human_reply", ReplyToID: "card"},
		loopd.Message{ID: "04", Kind: loopd.RoleOperator, Key: "other"})
	f.mu.Unlock()
	f.step(time.Second)
	f.mu.Lock()
	if len(f.questions) != 1 || f.committed != "" {
		t.Error("ordinary followup approved or overtook pending interaction")
	}
	f.mu.Unlock()
	f.resolve("01/answer-style", loopd.HumanResult{Status: loopd.HumanDismissed})
	f.step(time.Millisecond)
	f.step(time.Second)
	f.mu.Lock()
	if len(f.questions) != 2 || f.questions["02/answer-style"].ReplyToID != "02" || f.committed != "01" {
		t.Error("queued input did not start its own interaction")
	}
	f.mu.Unlock()
	f.resolve("02/answer-style", loopd.HumanResult{Status: loopd.HumanTimeout})
	f.step(time.Millisecond)
	f.step(time.Millisecond) // typed reply is observed via its Human handle, not another Ask
	f.step(time.Millisecond) // non-user input is outside this demo's policy
	f.step(0)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.questions) != 2 || len(f.answers) != 2 || f.committed != "04" {
		t.Fatalf("questions=%d answers=%d committed=%s",
			len(f.questions), len(f.answers), f.committed)
	}
}

// +case=`Speak 响应丢失或 Commit 失败后重读输入；不提前提交且汇总身份不变`
func TestRetryBeforeCommit(t *testing.T) {
	for _, stage := range []string{"speak", "commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t, userMessage("01", "delivery", "你好"))
			f.step(time.Second)
			f.resolve("01/answer-style", loopd.HumanResult{Status: loopd.HumanTimeout})
			f.mu.Lock()
			switch stage {
			case "speak":
				f.loseSpeakResponse = true
			case "commit":
				f.failPath = "/v1/conversations/conv/commit"
			}
			f.mu.Unlock()
			if _, err := f.reconcile(); (err != nil) != (stage == "commit") {
				t.Fatalf("stage %s: unexpected error %v", stage, err)
			}
			f.mu.Lock()
			wantCommitted := ""
			if stage == "speak" {
				wantCommitted = "01"
			}
			if f.committed != wantCommitted || len(f.answers) != 1 {
				t.Error("failure advanced consumption or lost summary")
			}
			f.mu.Unlock()
			if stage == "commit" {
				f.step(time.Millisecond)
			}
			f.step(0)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.committed != "01" || len(f.answers) != 1 || len(f.questions) != 1 {
				t.Fatalf("retry changed identity: committed=%s answers=%d questions=%d",
					f.committed, len(f.answers), len(f.questions))
			}
		})
	}
}
