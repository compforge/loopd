// Package interaction consumes conversation messages and demonstrates Ask/Confirm Verbs.
package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	loopd "github.com/compforge/loopd"
	rt "github.com/compforge/loopd/runtime"
	convapi "github.com/compforge/loopd/runtime/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const OperatorKey = "interaction"

var actor = loopd.ActorRef{Kind: loopd.RoleOperator, Key: OperatorKey}

type Reconciler struct{ loop rt.Loop }

func New(loop rt.Loop) *Reconciler { return &Reconciler{loop: loop} }

func (d *Reconciler) SetupWithManager(mgr manager.Manager) error {
	if err := convapi.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(OperatorKey).
		For(&convapi.Conversation{}, builder.WithPredicates(rt.ConversationPredicate(actor))).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(d)
}

// +spec=`每个 Conv 串行处理普通用户发言；Ask 成功后才 Confirm；Speak 成功后才 Commit；取消或超时不视为同意`
func (d *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	// Keep the input uncommitted while waiting. Re-reading it restores the
	// same question identities without process-local workflow state.
	inbox, err := d.loop.Conv.Poll(ctx, request.Name, loopd.PollRequest{Actor: actor, Limit: 1})
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(inbox.Messages) == 0 {
		return ctrl.Result{}, nil
	}
	message := inbox.Messages[0]
	if message.Kind == loopd.RoleUser && message.Purpose != "human_reply" {
		pending, err := d.interact(ctx, request.Name, message)
		if err != nil {
			return ctrl.Result{}, err
		}
		if pending {
			// Card replies wake this Conv. Timeouts also need a timer because
			// they do not create user messages. Release the reconcile slot.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
	// Card replies are already observed through Human handles. Consuming them
	// must not create another Ask and prompt the user indefinitely.
	if err := d.loop.Conv.Commit(ctx, request.Name, loopd.CommitRequest{Actor: actor, Through: inbox.Position}); err != nil {
		return ctrl.Result{}, err
	}
	slog.InfoContext(ctx, "interaction input committed", "conversation_id", request.Name,
		"message_id", message.ID, "purpose", message.Purpose)
	return ctrl.Result{RequeueAfter: time.Millisecond}, nil
}

func (d *Reconciler) interact(ctx context.Context, conversationID string, message loopd.Message) (bool, error) {
	var input struct {
		Blocks []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(message.Content, &input); err != nil {
		return false, fmt.Errorf("read demo message %s: %w", message.ID, err)
	}
	var parts []string
	for _, block := range input.Blocks {
		if block.Type == "text" && block.Content != "" {
			parts = append(parts, block.Content)
		}
	}
	query := strings.Join(parts, "\n")
	target := loopd.ActorRef{Kind: loopd.RoleUser, Key: message.Key}
	askPrompt := fmt.Sprintf("关于你的问题：%s\n你希望以哪种方式处理？", query)
	// Inputs stay identical across retries. The server owns the original
	// deadline and immutable result, so restarting never resets the 10s wait.
	ask, err := d.loop.Human.Ask(ctx, rt.AskRequest{
		ConversationID: conversationID, Actor: actor, Target: target, ReplyToID: message.ID,
		EffectKey: message.ID + "/answer-style", Timeout: 10 * time.Second,
		Title: "选择处理方式", Prompt: askPrompt,
		Choices: []loopd.HumanChoice{
			{Value: "brief", Label: "简要说明"},
			{Value: "steps", Label: "分步骤说明"},
			{Value: "examples", Label: "举例说明"},
		},
	})
	if err != nil {
		return false, err
	}
	choice, err := ask.Get(ctx)
	if err != nil {
		return false, err
	}
	if !choice.Status.Terminal() {
		return true, nil
	}
	lines := []string{"交互结果", "原始问题：" + query, "Ask：" + askPrompt, "你的选择：" + outcome(choice)}
	if choice.Status == loopd.HumanSuccess {
		prompt := "你选择了「" + outcome(choice) + "」。是否确认按这个方式处理？（本例只汇总交互，不调用模型或执行外部操作。）"
		confirm, err := d.loop.Human.Confirm(ctx, rt.ConfirmRequest{
			ConversationID: conversationID, Actor: actor, Target: target, ReplyToID: message.ID,
			EffectKey: message.ID + "/confirm-style", Timeout: 10 * time.Second,
			Title: "确认处理方式", Prompt: prompt, ConfirmLabel: "确认", DeclineLabel: "取消",
		})
		if err != nil {
			return false, err
		}
		decision, err := confirm.Get(ctx)
		if err != nil {
			return false, err
		}
		if !decision.Status.Terminal() {
			return true, nil
		}
		lines = append(lines, "Confirm："+prompt, "你的确认："+outcome(decision))
	} else {
		lines = append(lines, "Confirm：未发起，因为 Ask 未得到选择。")
	}
	content, err := json.Marshal(map[string]any{
		"version": "1.0", "biz": "chat", "meta": map[string]any{},
		"blocks": []map[string]any{{"id": "answer", "type": "text", "content": strings.Join(lines, "\n\n")}},
	})
	if err != nil {
		return false, err
	}
	// Stable message identity lets delivery or Commit retry without another answer.
	if _, err := d.loop.Conv.Speak(ctx, conversationID, loopd.SpeakRequest{
		Key: message.ID + "/summary", Actor: actor, Target: target, ReplyToID: message.ID,
		Content: content,
	}); err != nil {
		return false, err
	}
	return false, nil
}

func outcome(result loopd.HumanResult) string {
	switch result.Status {
	case loopd.HumanDismissed:
		return "已取消"
	case loopd.HumanTimeout:
		return "已超时（未在 10 秒内操作）"
	case loopd.HumanFailure:
		return "交互失败：" + result.Reason
	case loopd.HumanSuccess:
		switch result.Value {
		case "brief":
			return "简要说明"
		case "steps":
			return "分步骤说明"
		case "examples":
			return "举例说明"
		case "accepted":
			return "已确认"
		case "declined":
			return "已取消"
		}
	}
	return string(result.Status)
}
