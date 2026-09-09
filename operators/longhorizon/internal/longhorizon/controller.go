// Package longhorizon owns the Manager/Executor/Auditor business loop.
package longhorizon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	"github.com/compforge/loopd/pkg/contract"
	convapi "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	OperatorKey                      = "longhorizon"
	ActorManager  contract.ActorKind = "operator/longhorizon/manager"
	ActorExecutor contract.ActorKind = "operator/longhorizon/executor"
	ActorAuditor  contract.ActorKind = "operator/longhorizon/auditor"
	ConvLabel                        = "longhorizon.loopd.compforge.io/conversation"
	RunLabel                         = "longhorizon.loopd.compforge.io/run"
)

type Config struct {
	RunTimeout, RetentionTTL                                      time.Duration
	MaxRounds                                                     int32
	ManagerTarget, ExecutorTarget, AuditorTarget                  string
	ManagerTimeout, ExecutorTimeout, AuditorTimeout, HumanTimeout time.Duration
}

func (c Config) defaults() Config {
	if c.RunTimeout <= 0 {
		c.RunTimeout = 24 * time.Hour
	}
	if c.RetentionTTL <= 0 {
		c.RetentionTTL = 24 * time.Hour
	}
	if c.MaxRounds <= 0 {
		c.MaxRounds = 25
	}
	if c.ManagerTimeout <= 0 {
		c.ManagerTimeout = 5 * time.Minute
	}
	if c.ExecutorTimeout <= 0 {
		c.ExecutorTimeout = 30 * time.Minute
	}
	if c.AuditorTimeout <= 0 {
		c.AuditorTimeout = 5 * time.Minute
	}
	if c.HumanTimeout <= 0 {
		c.HumanTimeout = 30 * time.Minute
	}
	if c.ManagerTarget == "" {
		c.ManagerTarget = "manager"
	}
	if c.ExecutorTarget == "" {
		c.ExecutorTarget = "executor"
	}
	if c.AuditorTarget == "" {
		c.AuditorTarget = "auditor"
	}
	return c
}

type Controller struct {
	Client client.Client
	Reader client.Reader
	Loop   loopruntime.Loop
	Config Config
}

func Setup(mgr ctrl.Manager, loop loopruntime.Loop, config Config) error {
	c := &Controller{Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), Loop: loop, Config: config.defaults()}
	if c.Config.MaxRounds > 1000 {
		return errors.New("max rounds must not exceed 1000")
	}
	if err := lh.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := ctrl.NewControllerManagedBy(mgr).Named("longhorizon-ingress").For(&convapi.Conversation{}, builder.WithPredicates(loopruntime.ConversationPredicate(consumer()))).Complete(reconcile.Func(c.Ingress)); err != nil {
		return err
	}
	convToRuns := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var runs lh.RunList
		if err := c.Client.List(ctx, &runs, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{ConvLabel: obj.GetName()}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(runs.Items))
		for _, run := range runs.Items {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&run)})
		}
		return requests
	})
	if err := ctrl.NewControllerManagedBy(mgr).Named("longhorizon-manager").For(&lh.Run{}).Owns(&lh.Execution{}).Owns(&lh.Audit{}).Watches(&convapi.Conversation{}, convToRuns).Complete(reconcile.Func(c.Manager)); err != nil {
		return err
	}

	if err := ctrl.NewControllerManagedBy(mgr).Named("longhorizon-executor").For(&lh.Execution{}).Complete(reconcile.Func(c.Executor)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).Named("longhorizon-auditor").For(&lh.Audit{}).Complete(reconcile.Func(c.Auditor))
}

func reference(obj client.Object) lh.Reference {
	return lh.Reference{Name: obj.GetName(), UID: obj.GetUID()}
}
func actor(run *lh.Run, kind contract.ActorKind) contract.ActorRef {
	return contract.ActorRef{Kind: kind, Key: string(run.UID)}
}
func stepKey(run *lh.Run, round int32, role string) string {
	return fmt.Sprintf("%s/round/%d/%s", run.UID, round, role)
}
func waiting() (ctrl.Result, error) { return ctrl.Result{RequeueAfter: 2 * time.Second}, nil }
func clip(s string) string {
	if len(s) > 2000 {
		return strings.ToValidUTF8(s[:2000], "")
	}
	return s
}

func consumer() contract.ActorRef {
	return contract.ActorRef{Kind: contract.ActorKindOperator, Key: OperatorKey}
}
func recipient(run *lh.Run) contract.ActorRef {
	return contract.ActorRef{Kind: contract.ActorKindUser, Key: run.Spec.UserKey}
}

// live guards authoritative owner identities before issuing work.
func (c *Controller) live(ctx context.Context, namespace string, convRef lh.Reference, runRef *lh.Reference) (bool, error) {
	var conv convapi.Conversation
	if err := c.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: convRef.Name}, &conv); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if conv.UID != convRef.UID || conv.DeletionTimestamp != nil {
		return false, nil
	}
	if runRef != nil {
		var run lh.Run
		if err := c.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: runRef.Name}, &run); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		if run.UID != runRef.UID || run.DeletionTimestamp != nil || run.Spec.Conversation != convRef || terminal(run.Status.Phase) || (!run.Spec.DeadlineAt.IsZero() && !time.Now().Before(run.Spec.DeadlineAt.Time)) {
			return false, nil
		}
	}
	return true, nil
}
func terminal(phase string) bool {
	return phase == "Succeeded" || phase == "Stopped" || phase == "Failed"
}

// Ingress owns initial intake. While a Run is active its Manager owns further Polls.
//
// +spec=`同一 User conv 的输入在 Run 未收尾时归当前 Run；最终总结持久化并记录 FinishedAt 后，未消费输入才可创建新 Run。`
// +why=`LongHorizon 当前暂不自动识别新任务还是老任务继续；以业务生命周期划分 Run，而不是按消息主题或页面流划分。`
func (c *Controller) Ingress(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var conv convapi.Conversation
	if err := c.Reader.Get(ctx, req.NamespacedName, &conv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if conv.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	var runs lh.RunList
	// An uncached list prevents duplicate ownership after an acknowledged Create.
	if err := c.Reader.List(ctx, &runs, client.InNamespace(conv.Namespace), client.MatchingLabels{ConvLabel: conv.Name}); err != nil {
		return ctrl.Result{}, err
	}
	for _, run := range runs.Items {
		if run.Spec.Conversation == reference(&conv) && (run.DeletionTimestamp != nil || run.Status.FinishedAt == nil) {
			return waiting()
		}
	}
	participant, ok := loopruntime.Participant(&conv, consumer())
	if !ok {
		return ctrl.Result{}, nil
	}
	if participant.ConversationID == "" {
		return waiting()
	}
	polled, err := c.Loop.Conv.Poll(ctx, conv.Name, contract.PollRequest{Actor: consumer(), Limit: 1})
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(polled.Messages) == 0 {
		return ctrl.Result{}, nil
	}
	message := polled.Messages[0]
	if message.Kind != contract.ActorKindUser || message.IsHumanReply() {
		return ctrl.Result{RequeueAfter: time.Millisecond}, c.Loop.Conv.Commit(ctx, conv.Name, contract.CommitRequest{Actor: consumer(), Through: polled.Position})
	}
	history, err := c.priorMessages(ctx, conv.Name, message.ID)
	if err != nil {
		return ctrl.Result{}, err
	}
	goal := messageText(message)
	if len(goal) == 0 || len(goal) > 16000 {
		_, err := c.Loop.Conv.Speak(ctx, conv.Name, contract.SpeakRequest{Key: message.ID + "/invalid", Actor: consumer(), Target: contract.ActorRef{Kind: message.Kind, Key: message.Key}, ReplyToID: message.ID, Content: reportContent(report{Text: "Goal must contain 1–16000 bytes of text."}, "Invalid goal", "manager")})
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, c.Loop.Conv.Commit(ctx, conv.Name, contract.CommitRequest{Actor: consumer(), Through: polled.Position})
	}
	run := &lh.Run{ObjectMeta: metav1.ObjectMeta{Name: message.ID, Namespace: conv.Namespace, Labels: map[string]string{ConvLabel: conv.Name}}, Spec: lh.RunSpec{Conversation: reference(&conv), WorkspaceID: participant.ConversationID, UserKey: message.Key, DeadlineAt: metav1.NewTime(time.Now().Add(c.Config.RunTimeout)), InputMessageID: message.ID, Goal: goal, MaxRounds: c.Config.MaxRounds}}
	for _, m := range history {
		if m.ID == message.ID {
			continue
		}
		// Human facts and completed business reports are stable prompt context.
		if m.Kind != contract.ActorKindUser {
			if _, err := reportFrom(m); err != nil {
				continue
			}
		}
		run.Spec.ContextMessages = append(run.Spec.ContextMessages, lh.MessageReference{ConversationID: m.ConversationID, MessageID: m.ID})
	}
	if len(run.Spec.ContextMessages) > 20 {
		run.Spec.ContextMessages = run.Spec.ContextMessages[len(run.Spec.ContextMessages)-20:]
	}
	if err := ctrl.SetControllerReference(&conv, run, c.Client.Scheme(), controllerutil.WithBlockOwnerDeletion(false)); err != nil {
		return ctrl.Result{}, err
	}
	if active, err := c.live(ctx, conv.Namespace, reference(&conv), nil); err != nil || !active {
		return ctrl.Result{}, err
	}
	if err := c.Client.Create(ctx, run); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	return waiting()
}

type report struct {
	Text  string
	Error string
}

func messageText(m contract.Message) string {
	var model struct {
		Blocks []struct {
			Content  string                 `json:"content"`
			Type     string                 `json:"type"`
			Value    string                 `json:"value"`
			Outcome  string                 `json:"outcome"`
			Prompt   string                 `json:"prompt"`
			Choices  []contract.HumanChoice `json:"choices"`
			Question *contract.HumanBlock   `json:"question"`
		} `json:"blocks"`
	}
	if json.Unmarshal(m.Content, &model) != nil {
		return ""
	}
	var texts []string
	for _, b := range model.Blocks {
		if b.Content != "" {
			texts = append(texts, b.Content)
		} else if b.Prompt != "" {
			question := b.Prompt
			for _, choice := range b.Choices {
				question += fmt.Sprintf("\n%s: %s", choice.Value, choice.Label)
			}
			texts = append(texts, question)
		} else if b.Type == "human_reply" {
			if b.Question != nil {
				texts = append(texts, b.Question.Prompt)
				for _, choice := range b.Question.Choices {
					texts = append(texts, choice.Value+": "+choice.Label)
				}
			}
			texts = append(texts, strings.TrimSpace(b.Outcome+" "+b.Value))
		}
	}
	return strings.Join(texts, "\n")
}
func reportFrom(m contract.Message) (report, error) {
	result, err := contract.ExtractResult(m.Content)
	if err != nil {
		return report{}, err
	}
	if result != nil || m.Status == contract.MessageStatusFailed || m.Status == contract.MessageStatusCancelled || m.Status == contract.MessageStatusExpired {
		if !m.Ended() {
			return report{}, errors.New("Harness output is incomplete")
		}
		if m.Status != contract.MessageStatusCompleted {
			return report{Error: "Harness execution " + string(m.Status)}, nil
		}
		return report{Text: result.Text()}, nil
	}

	var model struct {
		Blocks []struct {
			Report  bool   `json:"longhorizon_report"`
			Content string `json:"content"`
			Error   string `json:"error"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(m.Content, &model); err != nil {
		return report{}, err
	}
	for _, b := range model.Blocks {
		if b.Report {
			return report{Text: b.Content, Error: b.Error}, nil
		}
	}
	return report{}, errors.New("sealed report has no report block")
}
func (c *Controller) readReport(ctx context.Context, conversationID, id string) (report, error) {
	m, err := c.message(ctx, conversationID, id)
	if err != nil {
		return report{}, err
	}
	if !m.Ended() {
		return report{}, errors.New("report message has not ended")
	}
	return reportFrom(m)
}
func reportContent(value report, title, role string) json.RawMessage {
	content, _ := json.Marshal(map[string]any{"version": "1.1", "biz": "chat", "meta": map[string]any{"title": title, "actor_display_name": role}, "blocks": []any{map[string]any{"id": "report", "type": "text", "content": value.Text, "error": value.Error, "longhorizon_report": true}}})
	return content
}

// invoke consumes the Server-owned result before its consumer updates a CRD.
// Repeating the submission restores the same durable Call.
func (c *Controller) invoke(ctx context.Context, run *lh.Run, round int32, kind contract.ActorKind, target, prompt string, timeout time.Duration) (report, string, string, bool, error) {
	ref := reference(run)
	if active, err := c.live(ctx, run.Namespace, run.Spec.Conversation, &ref); err != nil || !active {
		return report{}, "", "", false, err
	}
	role := strings.TrimPrefix(string(kind), "operator/longhorizon/")
	// One role Harness keeps its identity across rounds; calls have separate keys.
	author := contract.ActorRef{Kind: contract.ActorKind(fmt.Sprintf(contract.OperatorHarnessKindFormat, "longhorizon")), Key: string(run.UID) + "/" + role}
	key := stepKey(run, round, role)
	call, err := c.Loop.Harness.Prompt(ctx, loopruntime.Prompt{ConversationID: run.Spec.WorkspaceID, IdempotencyKey: key, EffectKey: fmt.Sprintf("round/%d/%s", round, role), Actor: &author, Recipient: recipient(run), Target: target, Text: prompt, Timeout: timeout, Meta: map[string]any{"title": fmt.Sprintf("Round %d · %s", round, role), "actor_display_name": role}})
	if err != nil {
		return report{}, "", "", false, err
	}
	value, err := call.Get(ctx)
	if err != nil {
		return report{}, "", call.ID(), false, err
	}
	if !value.Phase.Terminal() {
		return report{}, value.MessageID, value.ID, false, nil
	}
	return report{Text: value.Result.Text(), Error: value.Error}, value.MessageID, value.ID, true, nil
}

func reportStatus(result report) contract.MessageStatus {
	if result.Error != "" {
		return contract.MessageStatusFailed
	}
	return contract.MessageStatusCompleted
}

func decode[T any](text string, value *T) error {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = strings.TrimSpace(strings.TrimSuffix(text[i+1:], "```"))
		}
	}
	if len(text) > 32000 {
		return errors.New("role result exceeds 32000 bytes")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing content after role result")
	}
	return nil
}

func (c *Controller) history(ctx context.Context, run *lh.Run) (string, error) {
	// Keep most recent context within a prompt bound, with authors and explicit IDs.
	var messages []string
	remaining := 16000
	for i := len(run.Spec.ContextMessages) - 1; i >= 0; i-- {
		ref := run.Spec.ContextMessages[i]
		m, err := c.message(ctx, ref.ConversationID, ref.MessageID)
		if err != nil {
			return "", err
		}
		text := messageText(m)
		if len(text) > remaining {
			text = strings.ToValidUTF8(text[:remaining], "") + " [context truncated]"
		}
		messages = append(messages, fmt.Sprintf("[%s %s/%s] %s", m.ID, m.Kind, m.Key, text))
		remaining -= len(text)
		if remaining <= 0 {
			break
		}
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return strings.Join(messages, "\n"), nil
}

// priorMessages selects a bounded stable tail before the input using shared Read.
func (c *Controller) priorMessages(ctx context.Context, convID, before string) ([]contract.Message, error) {
	page, err := c.Loop.Conv.List(ctx, convID, loopruntime.MessageQuery{Before: before, Order: loopruntime.Desc, Limit: 20,
		Statuses: []contract.MessageStatus{contract.MessageStatusCompleted, contract.MessageStatusFailed, contract.MessageStatusCancelled, contract.MessageStatusExpired}})
	if err != nil {
		return nil, err
	}
	history := make([]contract.Message, 0, len(page.Messages))
	for i := len(page.Messages) - 1; i >= 0; i-- {
		m, err := page.Messages[i].Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		history = append(history, m)
	}
	return history, nil
}

// message resolves a saved reference directly without changing consumption.
func (c *Controller) message(ctx context.Context, convID, id string) (contract.Message, error) {
	return c.Loop.Conv.Read(convID, id).Snapshot(ctx)
}
