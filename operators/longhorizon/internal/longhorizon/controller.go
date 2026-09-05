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

	agentue "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	convapi "github.com/compforge/loopd/runtime/api/v1alpha1"
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
	OperatorKey                   = "longhorizon"
	ActorManager  loopd.ActorKind = "operator/longhorizon/manager"
	ActorExecutor loopd.ActorKind = "operator/longhorizon/executor"
	ActorAuditor  loopd.ActorKind = "operator/longhorizon/auditor"
	ConvLabel                     = "longhorizon.loopd.compforge.io/conversation"
	RunLabel                      = "longhorizon.loopd.compforge.io/run"
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
func actor(run *lh.Run, kind loopd.ActorKind) loopd.ActorRef {
	return loopd.ActorRef{Kind: kind, Key: string(run.UID)}
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

func consumer() loopd.ActorRef {
	return loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: OperatorKey}
}
func recipient(run *lh.Run) loopd.ActorRef {
	return loopd.ActorRef{Kind: loopd.ActorKindUser, Key: run.Spec.UserKey}
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
	polled, err := c.Loop.Conv.Poll(ctx, conv.Name, loopd.PollRequest{Actor: consumer(), Limit: 1})
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(polled.Messages) == 0 {
		return ctrl.Result{}, nil
	}
	message := polled.Messages[0]
	if message.Kind != loopd.ActorKindUser || message.Purpose != "input" {
		return ctrl.Result{RequeueAfter: time.Millisecond}, c.Loop.Conv.Commit(ctx, conv.Name, loopd.CommitRequest{Actor: consumer(), Through: polled.Position})
	}
	history, err := c.priorMessages(ctx, conv.Name, message.ID)
	if err != nil {
		return ctrl.Result{}, err
	}
	goal := messageText(message)
	if len(goal) == 0 || len(goal) > 16000 {
		_, err := c.Loop.Conv.Speak(ctx, conv.Name, loopd.SpeakRequest{Key: message.ID + "/invalid", Actor: consumer(), Target: loopd.ActorRef{Kind: message.Kind, Key: message.Key}, ReplyToID: message.ID, Content: reportContent(report{Text: "Goal must contain 1–16000 bytes of text."}, "Invalid goal", "manager")})
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, c.Loop.Conv.Commit(ctx, conv.Name, loopd.CommitRequest{Actor: consumer(), Through: polled.Position})
	}
	workspace, err := c.Loop.Conv.Workspace(ctx, conv.Name, consumer())
	if err != nil {
		return ctrl.Result{}, err
	}
	run := &lh.Run{ObjectMeta: metav1.ObjectMeta{Name: message.ID, Namespace: conv.Namespace, Labels: map[string]string{ConvLabel: conv.Name}}, Spec: lh.RunSpec{Conversation: reference(&conv), WorkspaceID: workspace.ID, UserKey: message.Key, DeadlineAt: metav1.NewTime(time.Now().Add(c.Config.RunTimeout)), InputMessageID: message.ID, Goal: goal, MaxRounds: c.Config.MaxRounds}}
	for _, m := range history {
		if m.ID == message.ID {
			continue
		}
		// Human facts and completed business reports are stable prompt context.
		if m.Purpose != "input" && m.Purpose != "human_reply" {
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

func messageText(m loopd.Message) string {
	var model struct {
		Blocks []struct {
			Content string              `json:"content"`
			Type    string              `json:"type"`
			Value   string              `json:"value"`
			Outcome string              `json:"outcome"`
			Prompt  string              `json:"prompt"`
			Choices []loopd.HumanChoice `json:"choices"`
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
			texts = append(texts, strings.TrimSpace(b.Outcome+" "+b.Value))
		}
	}
	return strings.Join(texts, "\n")
}
func reportFrom(m loopd.Message) (report, error) {
	var model struct {
		Blocks []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Error   string `json:"error"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(m.Content, &model); err != nil {
		return report{}, err
	}
	for _, b := range model.Blocks {
		if b.ID == "report" {
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
	content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat", "meta": map[string]any{"title": title, "actor_display_name": role}, "blocks": []any{map[string]any{"id": "report", "type": "text", "content": value.Text, "error": value.Error}}})
	return content
}

// invoke records the full business result in one persisted message event before
// its consumer updates a CRD. Execution recovery remains Adapter-owned.
func (c *Controller) invoke(ctx context.Context, run *lh.Run, round int32, kind loopd.ActorKind, target, prompt string, timeout time.Duration) (report, string, string, bool, error) {
	ref := reference(run)
	if active, err := c.live(ctx, run.Namespace, run.Spec.Conversation, &ref); err != nil || !active {
		return report{}, "", "", false, err
	}
	author := actor(run, kind)
	role := strings.TrimPrefix(string(kind), "operator/longhorizon/")
	key := stepKey(run, round, role)
	content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat", "meta": map[string]any{"title": fmt.Sprintf("Round %d · %s", round, role), "actor_display_name": role}, "blocks": []any{}})
	m, err := c.Loop.Conv.Speak(ctx, run.Spec.WorkspaceID, loopd.SpeakRequest{Stream: true, Key: key + "/report", Actor: author, Target: recipient(run), Content: content})
	if err != nil {
		return report{}, "", "", false, err
	}
	if value, err := reportFrom(m.Value()); err == nil {
		return value, m.ID(), "", true, m.End(ctx)
	}
	call, err := c.Loop.Harness.Prompt(ctx, loopruntime.Prompt{ConversationID: run.Spec.WorkspaceID, IdempotencyKey: key + "/call", EffectKey: key + "/call", Actor: &author, Target: target, Text: prompt, Timeout: timeout})
	if err != nil {
		return report{}, "", "", false, err
	}
	value := call.Value()
	if !value.Phase.Terminal() {
		return report{}, m.ID(), value.ID, false, nil
	}
	// The handle owns ordered retries and persists the report before End.
	err = m.Emit(ctx, agentue.Event{Op: agentue.OpSet, Block: map[string]any{"id": "report", "type": "text", "content": value.Result, "error": value.Error}})
	if err != nil {
		return report{}, m.ID(), value.ID, false, err
	}
	if err := m.End(ctx); err != nil {
		return report{}, m.ID(), value.ID, false, err
	}
	result, err := reportFrom(m.Value())
	return result, m.ID(), value.ID, true, err
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
		if m.Purpose == "human_reply" && m.ReplyToID != "" {
			question, err := c.message(ctx, ref.ConversationID, m.ReplyToID)
			if err != nil {
				return "", err
			}
			text = messageText(question) + "\nReply: " + text
		}
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
func (c *Controller) priorMessages(ctx context.Context, convID, before string) ([]loopd.Message, error) {
	var history []loopd.Message
	after := ""
	for {
		page, err := c.Loop.Conv.Read(ctx, convID, after, 100)
		if err != nil {
			return nil, err
		}
		for _, m := range page {
			if m.ID >= before {
				return history, nil
			}
			if m.Purpose == "input" || m.Purpose == "human_reply" || m.Ended() {
				history = append(history, m)
				if len(history) > 20 {
					history = history[len(history)-20:]
				}
			}
		}
		if len(page) < 100 {
			return history, nil
		}
		next := page[len(page)-1].ID
		if next <= after {
			return nil, errors.New("history pagination did not advance")
		}
		after = next
	}
}

// message resolves a saved reference without changing consumption or adding a Context verb.
func (c *Controller) message(ctx context.Context, convID, id string) (loopd.Message, error) {
	after := ""
	for {
		page, err := c.Loop.Conv.Read(ctx, convID, after, 100)
		if err != nil {
			return loopd.Message{}, err
		}
		for _, m := range page {
			if m.ID == id {
				return m, nil
			}
			if m.ID > id {
				return loopd.Message{}, fmt.Errorf("message %s not found in conversation %s", id, convID)
			}
		}
		if len(page) < 100 {
			return loopd.Message{}, fmt.Errorf("message %s not found in conversation %s", id, convID)
		}
		next := page[len(page)-1].ID
		if next <= after {
			return loopd.Message{}, errors.New("history pagination did not advance")
		}
		after = next
	}
}
