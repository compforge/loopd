package longhorizon

import (
	"context"
	"fmt"

	lh "github.com/compforge/loopd/operators/longhorizon/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *Controller) Executor(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var execution lh.Execution
	if err := c.Reader.Get(ctx, req.NamespacedName, &execution); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if execution.DeletionTimestamp != nil || execution.Status.MessageID != "" {
		return ctrl.Result{}, nil
	}
	var run lh.Run
	if err := c.Reader.Get(ctx, client.ObjectKey{Namespace: execution.Namespace, Name: execution.Spec.Run.Name}, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if live, err := c.live(ctx, execution.Namespace, run.Spec.Conversation, &execution.Spec.Run); err != nil || !live {
		return ctrl.Result{}, err
	}
	reports := ""
	for _, id := range execution.Spec.ReportMessageIDs {
		r, err := c.readReport(ctx, run.Spec.WorkspaceID, id)
		if err != nil {
			return ctrl.Result{}, err
		}
		reports += "\n" + r.Text
	}
	prompt := fmt.Sprintf("You are the LongHorizon Executor. Use the CLI/file tools to carry out only the current plan. Work in the provided workspace. Do not ask the human directly. Report actions, changed artifacts and limitations; your claims will be independently audited.\nContract:\n%s\nPlan:\n%s\nReferenced audits:\n%s", execution.Spec.Contract, execution.Spec.Plan, reports)
	result, message, call, done, err := c.invoke(ctx, &run, execution.Spec.Round, ActorExecutor, c.Config.ExecutorTarget, prompt, c.Config.ExecutorTimeout)
	if err != nil {
		return ctrl.Result{}, err
	}
	before := execution.DeepCopy()
	if call != "" {
		execution.Status.CallID = call
	}
	execution.Status.Phase = "Running"
	if done {
		execution.Status.MessageID = message
		execution.Status.Phase = "Completed"
		execution.Status.Error = clip(result.Error)
		if result.Error != "" {
			execution.Status.Phase = "Failed"
		}
	}
	if before.Status != execution.Status {
		if err := c.Client.Status().Patch(ctx, &execution, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	if !done {
		return waiting()
	}
	return ctrl.Result{}, nil
}

func (c *Controller) Auditor(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var audit lh.Audit
	if err := c.Reader.Get(ctx, req.NamespacedName, &audit); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if audit.DeletionTimestamp != nil || audit.Status.MessageID != "" {
		return ctrl.Result{}, nil
	}
	var run lh.Run
	if err := c.Reader.Get(ctx, client.ObjectKey{Namespace: audit.Namespace, Name: audit.Spec.Run.Name}, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if live, err := c.live(ctx, audit.Namespace, run.Spec.Conversation, &audit.Spec.Run); err != nil || !live {
		return ctrl.Result{}, err
	}
	execution, err := c.readReport(ctx, run.Spec.WorkspaceID, audit.Spec.ExecutionMessageID)
	if err != nil {
		return ctrl.Result{}, err
	}
	history, err := c.history(ctx, &run)
	if err != nil {
		return ctrl.Result{}, err
	}
	prompt := fmt.Sprintf(`You are the independent LongHorizon Auditor. Inspect the actual workspace using read-only tools. Executor statements are unverified claims. Check every original requirement and the saved final artifacts. The contract may not drop requirements from the original goal or human guidance; flag any such omission. Do not modify the environment. Return exactly one JSON object: {"complete":false,"integrity":"clean","task_state":"verified facts","evidence":"files and observations supporting the verdict","feedback":"remaining work"}. integrity is clean, violated or unknown. complete may be true only with concrete independent evidence that the full contract is satisfied.
Prior conversation (clarifies continuation requests):
%s
Original goal:
%s
Human guidance:
%s
Contract:
%s
Executor report (claims):
%s
Execution error:
%s`, history, run.Spec.Goal, run.Status.Guidance, audit.Spec.Contract, execution.Text, execution.Error)
	result, message, call, done, err := c.invoke(ctx, &run, audit.Spec.Round, ActorAuditor, c.Config.AuditorTarget, prompt, c.Config.AuditorTimeout)
	if err != nil {
		return ctrl.Result{}, err
	}
	before := audit.DeepCopy()
	if call != "" {
		audit.Status.CallID = call
	}
	audit.Status.Phase = "Running"
	if done {
		audit.Status.MessageID = message
		audit.Status.Phase = "Completed"
		audit.Status.Error = clip(result.Error)
		var verdict lh.Verdict
		if err := decode(result.Text, &verdict); err != nil {
			audit.Status.Error = clip(fmt.Sprintf("invalid audit: %v", err))
		} else if verdict.Integrity != "clean" && verdict.Integrity != "violated" && verdict.Integrity != "unknown" {
			audit.Status.Error = "invalid audit integrity"
		} else if verdict.Complete && (verdict.Evidence == "" || verdict.Integrity != "clean") {
			audit.Status.Error = "completion requires clean integrity and independent evidence"
		} else {
			verdict.TaskState = clip(verdict.TaskState)
			verdict.Evidence = clip(verdict.Evidence)
			verdict.Feedback = clip(verdict.Feedback)
			audit.Status.Verdict = &verdict
		}
		if audit.Status.Error != "" {
			audit.Status.Phase = "Failed"
			audit.Status.Verdict = nil
		}
	}
	if done || before.Status.CallID != audit.Status.CallID || before.Status.Phase != audit.Status.Phase {
		if err := c.Client.Status().Patch(ctx, &audit, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	if !done {
		return waiting()
	}
	return ctrl.Result{}, nil
}
