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
	"time"

	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	defaultMaxSubtasks = 4
	// OperatorKey identifies this participant in a conversation.
	OperatorKey = "router"
)

// Config controls the bounded temporary Harness fan-out used by the Router.
type Config struct {
	HarnessTarget string
	MaxSubtasks   int
	Logger        *slog.Logger
}

// Reconciler receives messages and runs the Router conversation loop.
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

// SetupWithManager watches this Router's conversation inbox. A reconciliation
// owns one active conversation loop; it calls Listen at execution boundaries.
func (reconciler *Reconciler) SetupWithManager(mgr manager.Manager, maxConcurrentReconciles int) error {
	if maxConcurrentReconciles <= 0 {
		return errors.New("Router reconcile concurrency must be positive")
	}
	return reconciler.loop.Conv.Watch(mgr, routerActor, reconciler,
		loopruntime.ConvWatchOptions{MaxConcurrentReconciles: maxConcurrentReconciles})
}

var routerActor = loopd.ActorRef{Kind: loopd.RoleOperator, Key: OperatorKey}

func (reconciler *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	// Receive one initial input. Further inputs join this execution only at the
	// boundary after its current Harness batch, rather than starting another task.
	inbox, err := reconciler.loop.Conv.Listen(ctx, request.Name, loopd.ListenRequest{Actor: routerActor, Limit: 1})
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(inbox.Messages) == 0 {
		return ctrl.Result{}, nil
	}
	message := inbox.Messages[0]
	if message.Kind != loopd.RoleUser || message.TaskID == "" {
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	chat, err := reconciler.loop.Chat.Context(ctx, message.TaskID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if chat.Conversation.ID != request.Name {
		return ctrl.Result{}, errors.New("Router input belongs to another conversation")
	}
	if chat.DeliveryState == "closed" {
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	return ctrl.Result{RequeueAfter: time.Millisecond}, reconciler.run(ctx, chat)
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
	case "summary":
		if len(result.Tasks) != 0 {
			return plan{}, errors.New("summary Router plan must not dispatch subtasks")
		}
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

func conversationText(task loopd.ChatContext) (string, error) {
	var lines []string
	for _, message := range task.History {
		if message.ID == task.Input.ID {
			continue
		}
		value, err := modelText(message.Content)
		if err != nil {
			// History also includes typed Human cards and non-text output.
			value = string(message.Content)
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
