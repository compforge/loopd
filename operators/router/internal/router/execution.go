package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
)

// run keeps execution within the conversation reconciliation, without a Work
// resource. This demo has no durable business checkpoint: Poll acknowledges
// receipt, not completion. Harness recovery remains the adapter's responsibility.
func (reconciler *Reconciler) run(ctx context.Context, input loopd.Message, messages []loopd.Message) (runErr error) {
	position := input.ID
	// UI streams are delivery identities, not business task boundaries.
	// Several published messages may contribute to the same input range.
	defer func() {
		if ctx.Err() != nil {
			return
		}
		if runErr != nil {
			reconciler.logger.ErrorContext(ctx, "Router execution failed",
				"conversation_id", input.ConversationID, "error", runErr)
		}
		if runErr != nil {
			// Failure is an explicit actor message, including when no UI stream exists.
			content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat",
				"meta":   map[string]any{"error": map[string]any{"code": "router_failed", "message": "Router 执行失败，请重试。"}},
				"blocks": []any{map[string]any{"id": "failure", "type": "text", "content": "Router 执行失败，请重试。"}}})
			if _, err := reconciler.loop.Conv.Speak(ctx, input.ConversationID, loopd.SpeakRequest{
				Key: input.ID + "/failure", Actor: routerActor,
				Target:    loopd.ActorRef{Kind: input.Kind, Key: input.Key},
				ReplyToID: input.ID, Content: content,
			}); err != nil {
				runErr = errors.Join(runErr, err)
				return
			}
		}
		runErr = errors.Join(runErr, reconciler.loop.Conv.Commit(ctx, input.ConversationID,
			loopd.CommitRequest{Actor: routerActor, Through: position}))
	}()
	workspace, err := reconciler.loop.Conv.Workspace(ctx, input.ConversationID, routerActor)
	if err != nil {
		return err
	}
	query, err := modelText(input.Content)
	if err != nil {
		return fmt.Errorf("read user query: %w", err)
	}
	history := conversationText(messages)
	var tasks, results []string
	summaries := 0
	for round := 0; ; round++ {
		prompt := planningPrompt(query, history, reconciler.maxSubtasks)
		if round > 0 {
			prompt = replanningPrompt(query, history, tasks, results, reconciler.maxSubtasks)
		}
		planKey := "plan"
		if round > 0 {
			planKey = fmt.Sprintf("plan/%d", round)
		}
		raw, err := reconciler.call(ctx, input, workspace.ID, planKey, prompt)
		if err != nil {
			return err
		}
		next, err := decodePlan(raw, query, reconciler.maxSubtasks)
		if err != nil {
			return err
		}
		if round == 0 && next.Kind == "summary" {
			return errors.New("initial Router plan must dispatch work")
		}
		reconciler.logger.InfoContext(ctx, "Router planned",
			"conversation_id", input.ConversationID, "round", round,
			"kind", next.Kind, "subtask_count", len(next.Tasks))
		if next.Kind != "summary" {
			batch, err := reconciler.executeBatch(ctx, input, workspace.ID, query, history, round, next.Tasks)
			if err != nil {
				return err
			}
			tasks = append(tasks, next.Tasks...)
			results = append(results, batch...)
		}
		// Drain inputs only after the dispatched batch has finished. Arrival
		// while waiting does not cancel/restart a Harness or launch another run.
		through := position
		additions, err := reconciler.pollAdditions(ctx, input.ConversationID, &position)
		if err != nil {
			return err
		}
		if len(additions) > 0 {
			content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat",
				"meta":   map[string]any{"through_id": through, "phase": "progress"},
				"blocks": []any{map[string]any{"id": "progress", "type": "text", "content": "阶段结果\n\n" + strings.Join(results, "\n\n")}}})
			if _, err := reconciler.loop.Conv.Speak(ctx, input.ConversationID, loopd.SpeakRequest{
				Key: fmt.Sprintf("%s/progress/%d", input.ID, round), Actor: routerActor,
				Target: loopd.ActorRef{Kind: input.Kind, Key: input.Key}, ReplyToID: input.ID, Content: content,
			}); err != nil {
				return err
			}
		}
		if len(additions) == 0 {
			summaryKey := "summarize"
			if summaries > 0 {
				summaryKey = fmt.Sprintf("summarize/%d", summaries)
			}
			summaries++
			answer, err := reconciler.call(ctx, input, workspace.ID, summaryKey, summaryPrompt(query, history, tasks, results))
			if err != nil {
				return err
			}
			// Publish this input snapshot even if more input arrived during
			// summarization. Those messages remain uncommitted for the next
			// reconciliation; continuous human input must not starve answers.
			{
				content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat",
					"meta":   map[string]any{"through_id": position},
					"blocks": []any{map[string]any{"id": "answer", "type": "text", "content": answer}}})
				_, err = reconciler.loop.Conv.Speak(ctx, input.ConversationID, loopd.SpeakRequest{
					Key: input.ID + "/answer", Actor: routerActor, Target: loopd.ActorRef{Kind: input.Kind, Key: input.Key},
					ReplyToID: input.ID, Content: content,
				})
				return err
			}
		}
		query += "\n\nAdditional user messages (in order):\n" + strings.Join(additions, "\n")
		reconciler.logger.InfoContext(ctx, "Router received additional input",
			"conversation_id", input.ConversationID, "message_count", len(additions))
	}
}

func (reconciler *Reconciler) pollAdditions(ctx context.Context, convID string, position *string) ([]string, error) {
	var texts []string
	const limit = 100
	for {
		inbox, err := reconciler.loop.Conv.Poll(ctx, convID, loopd.PollRequest{Actor: routerActor, Limit: limit, After: *position})
		if err != nil {
			return nil, err
		}
		if inbox.Position > *position {
			*position = inbox.Position
		}
		for _, message := range inbox.Messages {
			if message.Kind != loopd.ActorKindUser {
				continue
			}
			text, err := modelText(message.Content)
			if err != nil {
				return nil, fmt.Errorf("read additional message %q: %w", message.ID, err)
			}
			texts = append(texts, text)
		}
		if len(inbox.Messages) < limit {
			return texts, nil
		}
	}
}

func (reconciler *Reconciler) executeBatch(ctx context.Context, input loopd.Message, workspaceID, query, history string, round int, tasks []string) ([]string, error) {
	calls := make([]*loopruntime.Call, len(tasks))
	// Start the entire bounded batch before waiting, so independent work can
	// run concurrently. Round-specific keys avoid replaying an earlier plan.
	for index, task := range tasks {
		key := fmt.Sprintf("work/%d", index)
		if round > 0 {
			key = fmt.Sprintf("work/%d/%d", round, index)
		}
		call, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
			ConversationID: workspaceID, IdempotencyKey: input.ID + "/" + key, EffectKey: key, Target: reconciler.harnessTarget,
			Text: executionPrompt(query, history, task),
		})
		if err != nil {
			return nil, err
		}
		calls[index] = call
	}
	results := make([]string, len(calls))
	for index, call := range calls {
		result, err := call.Wait(ctx)
		if err != nil {
			return nil, err
		}
		results[index] = strings.TrimSpace(result.Result)
		if results[index] == "" {
			return nil, fmt.Errorf("Harness subtask %d returned an empty result", index+1)
		}
	}
	return results, nil
}

func (reconciler *Reconciler) call(ctx context.Context, input loopd.Message, workspaceID, key, prompt string) (string, error) {
	call, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
		ConversationID: workspaceID, IdempotencyKey: input.ID + "/" + key, EffectKey: key, Target: reconciler.harnessTarget, Text: prompt,
	})
	if err != nil {
		return "", fmt.Errorf("start %s Harness: %w", key, err)
	}
	result, err := call.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("wait for %s Harness: %w", key, err)
	}
	text := strings.TrimSpace(result.Result)
	if text == "" {
		return "", fmt.Errorf("%s Harness returned an empty result", key)
	}
	return text, nil
}

func replanningPrompt(query, history string, tasks, results []string, maxSubtasks int) string {
	return fmt.Sprintf(`You re-plan an active Router conversation after additional user input.
Use the completed results below; do not repeat work that already answers the updated request.
Return JSON only: {"kind":"summary|simple|complex","tasks":["self-contained task"]}.
Choose summary with an empty tasks array if the existing results suffice.
Otherwise choose simple (one task) or complex (two to %d independent tasks).
Each new task must include relevant facts from the completed results.

%s`, maxSubtasks, summaryPrompt(query, history, tasks, results))
}
