package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
)

// run keeps execution within the conversation reconciliation, without a Work
// resource. This demo has no durable business checkpoint: Listen acknowledges
// receipt, not completion. Harness recovery remains the adapter's responsibility.
func (reconciler *Reconciler) run(ctx context.Context, chat loopd.ChatContext) (runErr error) {
	deliveryIDs := []string{chat.ID}
	// All inputs consumed during this run share one answer, but each UI stream
	// must terminate. They are delivery identities, not separate business tasks.
	defer func() {
		if ctx.Err() != nil {
			return
		}
		var failure *loopruntime.TaskFailure
		if runErr != nil {
			failure = &loopruntime.TaskFailure{Code: "router_failed", Message: runErr.Error()}
			reconciler.logger.ErrorContext(ctx, "Router execution failed",
				"conversation_id", chat.Conversation.ID, "error", runErr)
		}
		var completionErr error
		for _, id := range deliveryIDs {
			completionErr = errors.Join(completionErr, reconciler.loop.Chat.Complete(ctx, id, failure))
		}
		runErr = errors.Join(runErr, completionErr)
	}()
	query, err := modelText(chat.Input.Content)
	if err != nil {
		return fmt.Errorf("read user query: %w", err)
	}
	history, err := conversationText(chat)
	if err != nil {
		return fmt.Errorf("read conversation history: %w", err)
	}
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
		raw, err := reconciler.call(ctx, chat.ID, planKey, prompt)
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
			"conversation_id", chat.Conversation.ID, "round", round,
			"kind", next.Kind, "subtask_count", len(next.Tasks))
		if next.Kind != "summary" {
			batch, err := reconciler.executeBatch(ctx, chat.ID, query, history, round, next.Tasks)
			if err != nil {
				return err
			}
			tasks = append(tasks, next.Tasks...)
			results = append(results, batch...)
		}
		// Drain inputs only after the dispatched batch has finished. Arrival
		// while waiting does not cancel/restart a Harness or launch another run.
		additions, err := reconciler.listenAdditions(ctx, chat.Conversation.ID, &deliveryIDs)
		if err != nil {
			return err
		}
		if len(additions) == 0 {
			summaryKey := "summarize"
			if summaries > 0 {
				summaryKey = fmt.Sprintf("summarize/%d", summaries)
			}
			summaries++
			answer, err := reconciler.call(ctx, chat.ID, summaryKey, summaryPrompt(query, history, tasks, results))
			if err != nil {
				return err
			}
			// Input can arrive while the summarizer is running too. Do not
			// publish a now-stale answer when another plan is needed.
			additions, err = reconciler.listenAdditions(ctx, chat.Conversation.ID, &deliveryIDs)
			if err != nil {
				return err
			}
			if len(additions) == 0 {
				_, err = reconciler.loop.Chat.Emit(ctx, chat.ID, agentueui.Event{
					Op:    agentueui.OpSet,
					Block: map[string]any{"id": "answer", "type": "text", "role": string(loopd.RoleOperator), "content": answer},
				})
				return err
			}
		}
		query += "\n\nAdditional user messages (in order):\n" + strings.Join(additions, "\n")
		reconciler.logger.InfoContext(ctx, "Router received additional input",
			"conversation_id", chat.Conversation.ID, "message_count", len(additions))
	}
}

func (reconciler *Reconciler) listenAdditions(ctx context.Context, convID string, deliveryIDs *[]string) ([]string, error) {
	var texts []string
	const limit = 100
	for {
		inbox, err := reconciler.loop.Conv.Listen(ctx, convID, loopd.ListenRequest{Actor: routerActor, Limit: limit})
		if err != nil {
			return nil, err
		}
		for _, message := range inbox.Messages {
			if message.Kind != loopd.RoleUser || message.TaskID == "" {
				continue
			}
			found := false
			for _, id := range *deliveryIDs {
				if id == message.TaskID {
					found = true
					break
				}
			}
			if !found {
				*deliveryIDs = append(*deliveryIDs, message.TaskID)
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

func (reconciler *Reconciler) executeBatch(ctx context.Context, chatID, query, history string, round int, tasks []string) ([]string, error) {
	calls := make([]*loopruntime.Call, len(tasks))
	// Start the entire bounded batch before waiting, so independent work can
	// run concurrently. Round-specific keys avoid replaying an earlier plan.
	for index, task := range tasks {
		key := fmt.Sprintf("work/%d", index)
		if round > 0 {
			key = fmt.Sprintf("work/%d/%d", round, index)
		}
		call, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
			TaskID: chatID, EffectKey: key, Target: reconciler.harnessTarget,
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

func (reconciler *Reconciler) call(ctx context.Context, chatID, key, prompt string) (string, error) {
	call, err := reconciler.loop.Harness.Prompt(ctx, loopruntime.Prompt{
		TaskID: chatID, EffectKey: key, Target: reconciler.harnessTarget, Text: prompt,
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
