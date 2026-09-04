// Package router implements the first loopd Operator: it plans one user
// request, fans complex work out to temporary Harness calls, and publishes one
// Operator-owned answer.
//
// The v1 Router does not discover or reserve registered Harnesses. Every call
// uses the configured transient Harness target. Once loopd has a Harness
// registry, selection and dispatch policy can evolve here without changing the
// loop-runtime contract.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	defaultMaxSubtasks = 4
	// OperatorKey is the Task target key handled by the Router.
	OperatorKey = "router"
)

// Config controls the bounded temporary Harness fan-out used by the Router.
type Config struct {
	HarnessTarget string
	MaxSubtasks   int
	Logger        *slog.Logger
}

// Reconciler turns a loopd Task into a plan, execution results, and final answer.
type Reconciler struct {
	loop          loopruntime.Loop
	harnessTarget string
	maxSubtasks   int
	logger        *slog.Logger
}

// New creates a Router Reconciler backed by one transient Harness target.
func New(loop loopruntime.Loop, config Config) (*Reconciler, error) {
	config.HarnessTarget = strings.TrimSpace(config.HarnessTarget)
	if config.HarnessTarget == "" {
		return nil, errors.New("Router Harness target is required")
	}
	if config.MaxSubtasks <= 0 {
		config.MaxSubtasks = defaultMaxSubtasks
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Reconciler{
		loop: loop, harnessTarget: config.HarnessTarget,
		maxSubtasks: config.MaxSubtasks, logger: config.Logger,
	}, nil
}

// SetupWithManager watches only Tasks addressed to this Router. Concurrency is
// explicit because one Reconcile may wait on several long-running Harnesses.
func (reconciler *Reconciler) SetupWithManager(mgr manager.Manager, maxConcurrentReconciles int) error {
	if maxConcurrentReconciles <= 0 {
		return errors.New("Router reconcile concurrency must be positive")
	}
	return reconciler.loop.Task.Watch(
		mgr,
		loopd.ActorRef{Kind: loopd.RoleOperator, Key: OperatorKey},
		reconciler,
		loopruntime.TaskWatchOptions{MaxConcurrentReconciles: maxConcurrentReconciles},
	)
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	task, err := reconciler.loop.Task.Get(ctx, request.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve Router task %q: %w", request.Name, err)
	}
	reconciler.logger.InfoContext(ctx, "Router task received",
		"task_id", task.ID,
		"conversation_id", task.Conversation.ID,
	)

	if err := reconciler.run(ctx, task); err != nil {
		if ctx.Err() != nil {
			return ctrl.Result{}, err
		}
		reconciler.logger.ErrorContext(ctx, "Router task failed", "task_id", task.ID, "error", err)
		completeErr := reconciler.loop.Chat.Complete(ctx, task.ID, &loopruntime.TaskFailure{
			Code: "router_failed", Message: err.Error(),
		})
		if completeErr != nil {
			return ctrl.Result{}, errors.Join(err, fmt.Errorf("complete failed Router task: %w", completeErr))
		}
		return ctrl.Result{}, nil
	}

	reconciler.logger.InfoContext(ctx, "Router task completed", "task_id", task.ID)
	return ctrl.Result{}, nil
}

func (reconciler *Reconciler) run(ctx context.Context, task loopd.TaskContext) error {
	query, err := modelText(task.Input.Content)
	if err != nil {
		return fmt.Errorf("read user query: %w", err)
	}
	history, err := conversationText(task)
	if err != nil {
		return fmt.Errorf("read conversation history: %w", err)
	}

	planCall, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
		TaskID: task.ID, EffectKey: "plan", Target: reconciler.harnessTarget,
		Text: planningPrompt(query, history, reconciler.maxSubtasks),
	})
	if err != nil {
		return fmt.Errorf("start planning Harness: %w", err)
	}
	planResult, err := planCall.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for planning Harness: %w", err)
	}
	plan, err := decodePlan(planResult.Result, query, reconciler.maxSubtasks)
	if err != nil {
		return err
	}
	reconciler.logger.InfoContext(ctx, "Router task planned",
		"task_id", task.ID,
		"complexity", plan.Kind,
		"subtask_count", len(plan.Tasks),
	)

	// Prompt returns immediately with a running handle. Starting every call
	// before waiting lets independent subtasks execute in parallel while their
	// AgentUE streams remain observable through the same Task.
	calls := make([]*loopruntime.Call, len(plan.Tasks))
	for index, subtask := range plan.Tasks {
		call, promptErr := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
			TaskID: task.ID, EffectKey: fmt.Sprintf("work/%d", index), Target: reconciler.harnessTarget,
			Text: executionPrompt(query, history, subtask),
		})
		if promptErr != nil {
			return fmt.Errorf("start Harness for subtask %d: %w", index+1, promptErr)
		}
		calls[index] = call
	}

	results := make([]string, len(calls))
	for index, call := range calls {
		result, waitErr := call.Wait(ctx)
		if waitErr != nil {
			return fmt.Errorf("wait for Harness subtask %d: %w", index+1, waitErr)
		}
		results[index] = strings.TrimSpace(result.Result)
		if results[index] == "" {
			return fmt.Errorf("Harness subtask %d returned an empty result", index+1)
		}
	}

	summaryCall, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
		TaskID: task.ID, EffectKey: "summarize", Target: reconciler.harnessTarget,
		Text: summaryPrompt(query, history, plan.Tasks, results),
	})
	if err != nil {
		return fmt.Errorf("start summary Harness: %w", err)
	}
	summary, err := summaryCall.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for summary Harness: %w", err)
	}
	answer := strings.TrimSpace(summary.Result)
	if answer == "" {
		return errors.New("summary Harness returned an empty answer")
	}
	if _, err := reconciler.loop.Chat.Emit(ctx, task.ID, agentueui.Event{
		Op: agentueui.OpSet,
		Block: map[string]any{
			"id": "answer", "type": "text", "role": string(loopd.RoleOperator), "content": answer,
		},
	}); err != nil {
		return fmt.Errorf("publish Router answer: %w", err)
	}
	if err := reconciler.loop.Chat.Complete(ctx, task.ID, nil); err != nil {
		return fmt.Errorf("complete Router task: %w", err)
	}
	return nil
}

type plan struct {
	Kind  string   `json:"kind"`
	Tasks []string `json:"tasks"`
}

func decodePlan(raw, query string, maxSubtasks int) (plan, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "```json") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "```json"))
		value = strings.TrimSpace(strings.TrimSuffix(value, "```"))
	} else if strings.HasPrefix(value, "```") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "```"))
		value = strings.TrimSpace(strings.TrimSuffix(value, "```"))
	}
	var result plan
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return plan{}, fmt.Errorf("decode Router plan: %w", err)
	}
	for index := range result.Tasks {
		result.Tasks[index] = strings.TrimSpace(result.Tasks[index])
		if result.Tasks[index] == "" {
			return plan{}, fmt.Errorf("Router plan subtask %d is empty", index+1)
		}
	}
	switch result.Kind {
	case "simple":
		if len(result.Tasks) == 0 {
			result.Tasks = []string{query}
		}
		if len(result.Tasks) != 1 {
			return plan{}, errors.New("simple Router plan must contain exactly one subtask")
		}
	case "complex":
		if len(result.Tasks) < 2 {
			return plan{}, errors.New("complex Router plan must contain at least two subtasks")
		}
	default:
		return plan{}, fmt.Errorf("Router plan has unsupported kind %q", result.Kind)
	}
	if len(result.Tasks) > maxSubtasks {
		return plan{}, fmt.Errorf("Router plan contains %d subtasks, limit is %d", len(result.Tasks), maxSubtasks)
	}
	return result, nil
}

func modelText(content json.RawMessage) (string, error) {
	var model struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(content, &model); err != nil {
		return "", err
	}
	var values []string
	var answer string
	for _, block := range model.Blocks {
		if block["type"] != "text" {
			continue
		}
		if value, ok := block["content"].(string); ok && strings.TrimSpace(value) != "" {
			value = strings.TrimSpace(value)
			if block["id"] == "answer" {
				answer = value
			}
			values = append(values, value)
		}
	}
	if answer != "" {
		return answer, nil
	}
	if len(values) == 0 {
		return "", errors.New("AgentUE model has no text content")
	}
	return strings.Join(values, "\n"), nil
}

func conversationText(task loopd.TaskContext) (string, error) {
	var lines []string
	for _, message := range task.History {
		if message.ID == task.Input.ID {
			continue
		}
		value, err := modelText(message.Content)
		if err != nil {
			return "", fmt.Errorf("message %q: %w", message.ID, err)
		}
		lines = append(lines, fmt.Sprintf("%s/%s: %s", message.Kind, message.Key, value))
	}
	if len(lines) == 0 {
		return "(no earlier messages)", nil
	}
	return strings.Join(lines, "\n"), nil
}

func planningPrompt(query, history string, maxSubtasks int) string {
	return fmt.Sprintf(`You plan work for a Router Operator.
Classify the current request as simple when one execution Harness can answer it coherently.
Classify it as complex only when it benefits from two or more independent subtasks that can run in parallel.
Return JSON only, using exactly this shape:
{"kind":"simple|complex","tasks":["self-contained task"]}
A simple plan must contain one task. A complex plan may contain at most %d tasks.

Conversation history:
%s

Current request:
%s`, maxSubtasks, history, query)
}

func executionPrompt(query, history, subtask string) string {
	return fmt.Sprintf(`You are a temporary execution Harness working for a Router Operator.
Complete the assigned subtask and return a self-contained factual result for the final synthesizer.

Conversation history:
%s

Original request:
%s

Assigned subtask:
%s`, history, query, subtask)
}

func summaryPrompt(query, history string, tasks, results []string) string {
	var evidence strings.Builder
	for index := range tasks {
		fmt.Fprintf(&evidence, "\n[%d] Task: %s\nResult: %s\n", index+1, tasks[index], results[index])
	}
	return fmt.Sprintf(`You synthesize the final answer for a Router Operator.
Answer the user's current request directly from the execution results. Reconcile overlap or conflicts, preserve useful detail, and do not mention routing, subtasks, or Harnesses unless the user asks.

Conversation history:
%s

Current request:
%s

Execution results:%s`, history, query, evidence.String())
}
