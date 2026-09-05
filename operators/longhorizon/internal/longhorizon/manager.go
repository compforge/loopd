package longhorizon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	loopd "github.com/compforge/loopd"
	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (c *Controller) Manager(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var run lh.Run
	if err := c.Reader.Get(ctx, req.NamespacedName, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if run.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	if live, err := c.live(ctx, run.Namespace, run.Spec.Conversation, nil); err != nil || !live {
		return ctrl.Result{}, err
	}
	if err := c.cleanup(ctx, &run); err != nil {
		return ctrl.Result{}, err
	}
	before := run.DeepCopy()
	if run.Status.Phase == "" {
		run.Status.Phase = "Receiving"
		run.Status.Round = 1
		run.Status.Budget = run.Spec.MaxRounds
		if run.Status.Budget <= 0 {
			run.Status.Budget = c.Config.MaxRounds
		}
		run.Status.Contract = run.Spec.Goal
		run.Status.ContractVersion = 1
		run.Status.InputThrough = run.Spec.InputMessageID
		run.Status.InputMessageIDs = []string{run.Spec.InputMessageID}
		return c.commit(ctx, before, &run)
	}
	// Persisted input checkpoint precedes Commit; retry it before any new work.
	if run.Status.InputThrough != "" {
		if err := c.Loop.Conv.Commit(ctx, run.Spec.Conversation.Name, loopd.CommitRequest{Actor: consumer(), Through: run.Status.InputThrough}); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !terminal(run.Status.Phase) && !run.Spec.DeadlineAt.IsZero() && !time.Now().Before(run.Spec.DeadlineAt.Time) {
		run.Status.Phase = "Failed"
		run.Status.Summary = "LongHorizon Run deadline exceeded."
		return c.commit(ctx, before, &run)
	}
	switch run.Status.Phase {
	case "Succeeded", "Stopped", "Failed":
		return c.finish(ctx, &run)
	case "Receiving":
		return c.receive(ctx, &run)
	case "WaitingForHuman":
		return c.human(ctx, &run)
	case "Executing":
		return c.observeExecution(ctx, &run)
	case "Auditing":
		return c.observeAudit(ctx, &run)
	case "Planning":
		if run.Status.Round > run.Status.Budget {
			if run.Status.Budget >= 1000 {
				run.Status.Phase = "Stopped"
				run.Status.Summary = "Maximum round budget reached."
			} else {
				run.Status.Phase = "WaitingForHuman"
				run.Status.HumanReason = "budget"
			}
			return c.commit(ctx, before, &run)
		}
	default:
		return ctrl.Result{}, fmt.Errorf("unknown Run phase %q", run.Status.Phase)
	}
	history, err := c.history(ctx, &run)
	if err != nil {
		return ctrl.Result{}, err
	}
	input, _ := json.Marshal(struct {
		History   string            `json:"prior_conversation"`
		Goal      string            `json:"goal"`
		Contract  string            `json:"contract"`
		TaskState string            `json:"verified_task_state"`
		Guidance  string            `json:"human_guidance,omitempty"`
		Audit     *lh.AuditEvidence `json:"last_audit,omitempty"`
	}{history, run.Spec.Goal, run.Status.Contract, run.Status.TaskState, run.Status.Guidance, run.Status.LastAudit})
	prompt := `You are the LongHorizon Manager. Preserve all original requirements in the contract. Plan one bounded CLI step at a time. Only independent audit evidence establishes completion; Executor claims do not. Use ask or blocked when human input is required. Return exactly one JSON object with next (cli, ask, blocked, done), summary, and optional plan, contract, question, choices [{value,label}], allow_other. For cli provide a concrete plan; for ask/blocked provide a question and choices or allow_other=true. Do not execute tools yourself.
Current facts:
` + string(input)
	result, message, _, done, err := c.invoke(ctx, &run, run.Status.Round, ActorManager, c.Config.ManagerTarget, prompt, c.Config.ManagerTimeout)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !done {
		return waiting()
	}
	run.Status.ManagerMessageID = message
	var decision lh.Decision
	if result.Error != "" {
		return c.failure(ctx, before, &run, result.Error)
	}
	if err := decode(result.Text, &decision); err != nil {
		return c.failure(ctx, before, &run, "Invalid Manager decision: "+err.Error())
	}
	if len(decision.Contract) > 16000 || len(decision.Plan) > 16000 || len(decision.Question) > 4000 || len(decision.Choices) > 20 {
		return c.failure(ctx, before, &run, "Manager decision exceeds its input bounds.")
	}
	if decision.Contract != "" && decision.Contract != run.Status.Contract {
		run.Status.Contract = decision.Contract
		run.Status.ContractVersion++
	}
	run.Status.Decision = &decision
	switch decision.Next {
	case "cli":
		if decision.Plan == "" {
			return c.failure(ctx, before, &run, "Executor plan is empty.")
		}
		execution := &lh.Execution{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-r%d", run.Name, run.Status.Round), Namespace: run.Namespace, Labels: map[string]string{RunLabel: run.Name}}, Spec: lh.ExecutionSpec{Run: reference(&run), Round: run.Status.Round, Contract: run.Status.Contract, ContractVersion: run.Status.ContractVersion, Plan: decision.Plan}}
		if run.Status.LastAudit != nil {
			execution.Spec.ReportMessageIDs = []string{run.Status.LastAudit.MessageID}
		}
		if err := ctrl.SetControllerReference(&run, execution, c.Client.Scheme(), controllerutil.WithBlockOwnerDeletion(false)); err != nil {
			return ctrl.Result{}, err
		}
		if err := c.Client.Create(ctx, execution); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
			var existing lh.Execution
			if err := c.Reader.Get(ctx, client.ObjectKeyFromObject(execution), &existing); err != nil {
				return ctrl.Result{}, err
			}
			if !reflect.DeepEqual(existing.Spec, execution.Spec) {
				return ctrl.Result{}, errors.New("execution identity conflicts with issued inputs")
			}
			execution = &existing
		}
		ref := reference(execution)
		run.Status.Execution = &ref
		run.Status.LastAudit = nil
		run.Status.Phase = "Executing"
	case "ask", "blocked":
		request := loopd.HumanRequest{ConversationID: run.Spec.Conversation.Name, Actor: actor(&run, ActorManager), Target: recipient(&run), ReplyToID: run.Spec.InputMessageID, EffectKey: "validate", Type: "ask", Title: "LongHorizon", Prompt: decision.Question, Timeout: c.Config.HumanTimeout, AllowOther: decision.AllowOther}
		for _, choice := range decision.Choices {
			request.Choices = append(request.Choices, loopd.HumanChoice{Value: choice.Value, Label: choice.Label})
		}
		if err := request.Validate(); err != nil {
			return c.failure(ctx, before, &run, "Invalid human question: "+err.Error())
		}
		run.Status.Phase = "WaitingForHuman"
		run.Status.HumanReason = "ask"
	case "done":
		if !canFinish(&run) {
			return c.failure(ctx, before, &run, "Completion rejected: no current, clean, complete audit evidence.")
		}
		appendRound(&run, "done", decision.Summary, []string{message})
		run.Status.Phase = "Succeeded"
		run.Status.Summary = decision.Summary
	default:
		return c.failure(ctx, before, &run, "Unsupported Manager decision: "+decision.Next)
	}
	return c.commit(ctx, before, &run)
}

func canFinish(run *lh.Run) bool {
	a := run.Status.LastAudit
	return a != nil && a.MessageID != "" && a.Execution.UID != "" && a.ContractVersion == run.Status.ContractVersion && a.Verdict.Complete && a.Verdict.Integrity == "clean" && a.Verdict.Evidence != ""
}
func (c *Controller) commit(ctx context.Context, before, run *lh.Run) (ctrl.Result, error) {
	succeeded, waiting := metav1.ConditionFalse, metav1.ConditionFalse
	if run.Status.Phase == "Succeeded" {
		succeeded = metav1.ConditionTrue
	}
	if run.Status.Phase == "WaitingForHuman" {
		waiting = metav1.ConditionTrue
	}
	for kind, value := range map[string]metav1.ConditionStatus{"Succeeded": succeeded, "WaitingForHuman": waiting} {
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: kind, Status: value, Reason: run.Status.Phase, Message: clip(run.Status.Summary), ObservedGeneration: run.Generation})
	}
	if err := c.Client.Status().Patch(ctx, run, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: time.Millisecond}, nil
}
func appendRound(run *lh.Run, decision, summary string, messages []string) {
	run.Status.Rounds = append(run.Status.Rounds, lh.RoundSummary{Index: run.Status.Round, Decision: decision, Summary: clip(summary), Messages: messages, Execution: run.Status.Execution, Audit: run.Status.Audit})
	if len(run.Status.Rounds) > 50 {
		run.Status.Rounds = run.Status.Rounds[len(run.Status.Rounds)-50:]
	}
}
func advance(run *lh.Run) {
	run.Status.ConsumedThrough = run.Status.Round
	run.Status.Round++
	run.Status.Execution = nil
	run.Status.Audit = nil
	run.Status.Decision = nil
	run.Status.ManagerMessageID = ""
	run.Status.HumanMessageID = ""
	run.Status.HumanReason = ""
	run.Status.Phase = "Receiving"
}
func (c *Controller) failure(ctx context.Context, before, run *lh.Run, reason string) (ctrl.Result, error) {
	appendRound(run, "failure", reason, []string{run.Status.ManagerMessageID})
	advance(run)
	run.Status.Failures++
	run.Status.Summary = clip(reason)
	if run.Status.Failures >= 3 {
		run.Status.Phase = "WaitingForHuman"
		run.Status.HumanReason = "failure"
	}
	return c.commit(ctx, before, run)
}

func (c *Controller) observeExecution(ctx context.Context, run *lh.Run) (ctrl.Result, error) {
	if run.Status.Execution == nil {
		return ctrl.Result{}, errors.New("Run has no execution reference")
	}
	var execution lh.Execution
	if err := c.Reader.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Status.Execution.Name}, &execution); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if execution.UID != run.Status.Execution.UID || execution.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	if execution.Status.MessageID == "" {
		return waiting()
	}
	if _, err := c.readReport(ctx, run.Spec.WorkspaceID, execution.Status.MessageID); err != nil {
		return ctrl.Result{}, err
	}
	before := run.DeepCopy()
	audit := &lh.Audit{ObjectMeta: metav1.ObjectMeta{Name: execution.Name, Namespace: run.Namespace, Labels: map[string]string{RunLabel: run.Name}}, Spec: lh.AuditSpec{Run: reference(run), Round: run.Status.Round, Contract: execution.Spec.Contract, ContractVersion: execution.Spec.ContractVersion, Execution: reference(&execution), ExecutionMessageID: execution.Status.MessageID}}
	if err := ctrl.SetControllerReference(run, audit, c.Client.Scheme(), controllerutil.WithBlockOwnerDeletion(false)); err != nil {
		return ctrl.Result{}, err
	}
	if err := c.Client.Create(ctx, audit); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
		var existing lh.Audit
		if err := c.Reader.Get(ctx, client.ObjectKeyFromObject(audit), &existing); err != nil {
			return ctrl.Result{}, err
		}
		if !reflect.DeepEqual(existing.Spec, audit.Spec) {
			return ctrl.Result{}, errors.New("audit identity conflicts with issued inputs")
		}
		audit = &existing
	}
	ref := reference(audit)
	run.Status.Audit = &ref
	run.Status.Phase = "Auditing"
	return c.commit(ctx, before, run)
}
func (c *Controller) observeAudit(ctx context.Context, run *lh.Run) (ctrl.Result, error) {
	if run.Status.Audit == nil {
		return ctrl.Result{}, errors.New("Run has no audit reference")
	}
	var audit lh.Audit
	if err := c.Reader.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Status.Audit.Name}, &audit); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if audit.UID != run.Status.Audit.UID || audit.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	if audit.Status.MessageID == "" {
		return waiting()
	}
	before := run.DeepCopy()
	if audit.Spec.Run != reference(run) || run.Status.Execution == nil || audit.Spec.Execution != *run.Status.Execution || audit.Spec.ContractVersion != run.Status.ContractVersion {
		return c.failure(ctx, before, run, "Stale audit references.")
	}
	if _, err := c.readReport(ctx, run.Spec.WorkspaceID, audit.Status.MessageID); err != nil {
		return ctrl.Result{}, err
	}
	if audit.Status.Verdict == nil || audit.Status.Error != "" {
		return c.failure(ctx, before, run, "Audit failed: "+audit.Status.Error)
	}
	verdict := *audit.Status.Verdict
	run.Status.LastAudit = &lh.AuditEvidence{Execution: audit.Spec.Execution, ContractVersion: audit.Spec.ContractVersion, MessageID: audit.Status.MessageID, Verdict: verdict}
	run.Status.TaskState = verdict.TaskState
	executionReport, err := c.readReport(ctx, run.Spec.WorkspaceID, audit.Spec.ExecutionMessageID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if executionReport.Error != "" {
		run.Status.Failures++
	} else {
		run.Status.Failures = 0
	}
	appendRound(run, "cli", verdict.Feedback, []string{run.Status.ManagerMessageID, audit.Spec.ExecutionMessageID, audit.Status.MessageID})
	advance(run)
	if run.Status.Failures >= 3 {
		run.Status.Phase = "WaitingForHuman"
		run.Status.HumanReason = "failure"
	}
	return c.commit(ctx, before, run)
}

func (c *Controller) human(ctx context.Context, run *lh.Run) (ctrl.Result, error) {
	before := run.DeepCopy()
	if run.Status.HumanMessageID == "" {
		author := actor(run, ActorManager)
		var handle *loopruntime.HumanHandle
		var err error
		key := stepKey(run, run.Status.Round, "human-"+run.Status.HumanReason)
		if run.Status.HumanReason == "ask" {
			d := run.Status.Decision
			if d == nil {
				return ctrl.Result{}, errors.New("missing human decision")
			}
			request := loopruntime.AskRequest{ConversationID: run.Spec.Conversation.Name, Actor: author, Target: recipient(run), ReplyToID: run.Spec.InputMessageID, EffectKey: key, Title: "LongHorizon needs your input", Prompt: d.Question, Timeout: c.Config.HumanTimeout, AllowOther: d.AllowOther}
			for _, choice := range d.Choices {
				request.Choices = append(request.Choices, loopd.HumanChoice{Value: choice.Value, Label: choice.Label})
			}
			handle, err = c.Loop.Human.Ask(ctx, request)
		} else {
			prompt := "Several attempts failed. Continue trying within this Run?"
			if run.Status.HumanReason == "budget" {
				prompt = fmt.Sprintf("The %d-round budget is exhausted. Add 25 rounds within this Run's existing deadline?", run.Status.Budget)
			}
			handle, err = c.Loop.Human.Confirm(ctx, loopruntime.ConfirmRequest{ConversationID: run.Spec.Conversation.Name, Actor: author, Target: recipient(run), ReplyToID: run.Spec.InputMessageID, EffectKey: key, Title: "Continue LongHorizon?", Prompt: prompt, Timeout: c.Config.HumanTimeout, ConfirmLabel: "Continue", DeclineLabel: "Finish here"})
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		run.Status.HumanMessageID = handle.ID()
		return c.commit(ctx, before, run)
	}
	result, err := c.Loop.Human.Get(ctx, run.Status.HumanMessageID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result.Status == loopd.HumanPending {
		return waiting()
	}
	accepted := result.Status == loopd.HumanSuccess && (run.Status.HumanReason == "ask" || result.Value == "accepted")
	if !accepted {
		appendRound(run, "human", "Human interaction ended: "+string(result.Status)+" "+result.Value, []string{run.Status.HumanMessageID})
		run.Status.Phase = "Stopped"
		run.Status.Summary = "Stopped after human interaction: " + string(result.Status) + " " + result.Value
		return c.commit(ctx, before, run)
	}
	switch run.Status.HumanReason {
	case "ask":
		guidance := run.Status.Guidance
		if guidance != "" {
			guidance += "\n"
		}
		answer := result.Value
		if decision := run.Status.Decision; decision != nil {
			for _, choice := range decision.Choices {
				if choice.Value == result.Value {
					answer += " (" + choice.Label + ")"
					break
				}
			}
			answer = "Question: " + decision.Question + "\nAnswer: " + answer
		}
		guidance += answer
		if len(guidance) > 16000 {
			appendRound(run, "ask", "Human guidance exceeds this Run's input limit.", []string{run.Status.HumanMessageID})
			run.Status.Phase = "Stopped"
			run.Status.Summary = "Human guidance exceeds this Run's input limit. Continue in a new Run using the saved messages."
			return c.commit(ctx, before, run)
		}
		run.Status.Guidance = guidance
		run.Status.LastAudit = nil
		ids := []string{run.Status.ManagerMessageID, run.Status.HumanMessageID}
		if result.Reply != nil {
			ids = append(ids, result.Reply.ID)
		}
		appendRound(run, "ask", clip(result.Value), ids)
		advance(run)
	case "budget":
		run.Status.Budget = min(int32(1000), run.Status.Budget+25)
	case "failure":
		run.Status.Failures = 0
	}
	run.Status.HumanMessageID = ""
	run.Status.HumanReason = ""
	run.Status.Phase = "Receiving"
	return c.commit(ctx, before, run)
}

// receive is the safe boundary between rounds. Input arriving while a Harness
// is running remains pending until the current execution and audit finish.
func (c *Controller) receive(ctx context.Context, run *lh.Run) (ctrl.Result, error) {
	before := run.DeepCopy()
	if len(run.Status.InputMessageIDs) < 100 {
		polled, err := c.Loop.Conv.Poll(ctx, run.Spec.Conversation.Name, loopd.PollRequest{Actor: consumer(), After: run.Status.InputThrough, Limit: 32})
		if err != nil {
			return ctrl.Result{}, err
		}
		for _, m := range polled.Messages {
			if m.Kind == loopd.ActorKindUser && m.Purpose == "input" {
				text := messageText(m)
				if m.Key != run.Spec.UserKey || len(run.Status.InputMessageIDs) >= 100 || len(run.Status.Guidance)+len(text)+len(m.ID)+8 > 16000 {
					break
				}
				run.Status.Guidance += "\n[" + m.ID + "] " + text
				run.Status.InputMessageIDs = append(run.Status.InputMessageIDs, m.ID)
				run.Status.ContractVersion++
				run.Status.LastAudit = nil
			}
			// Non-input messages are observed, never interpreted as human approval.
			run.Status.InputThrough = m.ID
		}
	}
	run.Status.Phase = "Planning"
	return c.commit(ctx, before, run)
}

func (c *Controller) finish(ctx context.Context, run *lh.Run) (ctrl.Result, error) {
	if run.Status.FinishedAt != nil {
		remaining := time.Until(run.Status.FinishedAt.Add(c.Config.RetentionTTL))
		if remaining > 0 {
			return ctrl.Result{RequeueAfter: remaining}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(c.Client.Delete(ctx, run, client.Preconditions{UID: &run.UID}, client.PropagationPolicy(metav1.DeletePropagationBackground)))
	}
	if run.Status.FinalMessageID == "" {
		m, err := c.Loop.Conv.Speak(ctx, run.Spec.Conversation.Name, loopd.SpeakRequest{Key: string(run.UID) + "/final", Actor: actor(run, ActorManager), Target: recipient(run), ReplyToID: run.Spec.InputMessageID, Content: reportContent(report{Text: run.Status.Summary}, "LongHorizon · "+run.Status.Phase, "manager")})
		if err != nil {
			return ctrl.Result{}, err
		}
		before := run.DeepCopy()
		run.Status.FinalMessageID = m.ID()
		return c.commit(ctx, before, run)
	}
	before := run.DeepCopy()
	now := metav1.Now()
	run.Status.FinishedAt = &now
	return c.commit(ctx, before, run)
}

// Children are collectible only after Manager has recorded consumption of their round.
func (c *Controller) cleanup(ctx context.Context, run *lh.Run) error {
	if run.Status.ConsumedThrough == 0 {
		return nil
	}
	var executions lh.ExecutionList
	var audits lh.AuditList
	opts := []client.ListOption{client.InNamespace(run.Namespace), client.MatchingLabels{RunLabel: run.Name}}
	if err := c.Client.List(ctx, &executions, opts...); err != nil {
		return err
	}
	if err := c.Client.List(ctx, &audits, opts...); err != nil {
		return err
	}
	for i := range executions.Items {
		item := &executions.Items[i]
		if item.Spec.Run == reference(run) && item.Spec.Round <= run.Status.ConsumedThrough && item.Status.MessageID != "" {
			if err := client.IgnoreNotFound(c.Client.Delete(ctx, item, client.Preconditions{UID: &item.UID})); err != nil {
				return err
			}
		}
	}
	for i := range audits.Items {
		item := &audits.Items[i]
		if item.Spec.Run == reference(run) && item.Spec.Round <= run.Status.ConsumedThrough && item.Status.MessageID != "" {
			if err := client.IgnoreNotFound(c.Client.Delete(ctx, item, client.Preconditions{UID: &item.UID})); err != nil {
				return err
			}
		}
	}
	return nil
}
