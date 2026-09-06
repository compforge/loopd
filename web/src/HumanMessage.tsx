import { useState } from "react";
import { replyHuman, type HumanResult, type Message } from "./api";
import { humanStatus } from "./human";
import type { HumanCard } from "./card";

/** @spec 问题与答复复用选项卡片；终态保留选中项且只读，操作始终引用问题消息。 */
export function HumanMessage({ message, card, onReply }: {
  message: Message; card: HumanCard; onReply?(result: HumanResult): void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [submittingValue, setSubmittingValue] = useState<string>();
  const [error, setError] = useState<string>();
  const question = card.question;
  const pending = card.mode === "request" && card.editable && question.status === "pending" && !!onReply;
  const choices = card.type === "confirm" ? [
    { value: "accepted", label: question.confirm_label || "同意" },
    { value: "declined", label: question.decline_label || "拒绝" },
  ] : question.choices ?? [];
  const selected = busy ? submittingValue : card.selected_value;
  const otherAnswer = card.selected_value !== undefined && !choices.some((choice) => choice.value === card.selected_value);
  async function answer(outcome: "success" | "dismissed", value?: string) {
    if (busy || !pending) return;
    setBusy(true); setSubmittingValue(value); setError(undefined);
    try { onReply?.(await replyHuman(message, { reply_to_id: card.question_id, outcome, value })); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setBusy(false); setSubmittingValue(undefined); }
  }
  return <div className={`human-question human-${card.mode}`} onKeyDown={(event) => {
    if (event.key === "Escape" && pending) { event.preventDefault(); event.stopPropagation(); void answer("dismissed"); }
  }}>
    <strong>{question.title}</strong>
    <p>{question.prompt}</p>
    <div className="human-status">{card.mode === "reply" ? "你的答复 · " : ""}{humanStatus(question.status)} · 截止 {new Date(question.deadline).toLocaleString()}</div>
    {choices.length > 0 && <fieldset className="human-options" disabled={busy || !pending} aria-label={question.title}>
      {choices.map((choice) => <label className={`human-choice${selected === choice.value ? " is-selected" : ""}`} key={choice.value}>
        <input type="radio" name={`human-${message.id}`} value={choice.value} checked={selected === choice.value}
          onChange={() => void answer("success", choice.value)} />
        <span><span>{choice.label}</span>{"description" in choice && choice.description && <small>{choice.description}</small>}</span>
      </label>)}
    </fieldset>}
    {otherAnswer && <div className="human-answer"><small>{choices.length ? "其他回答" : "回答"}</small><p>{card.selected_value}</p></div>}
    {pending && <fieldset disabled={busy}>
      {card.type === "ask" && question.allow_other && <form onSubmit={(event) => { event.preventDefault(); if (text.trim()) void answer("success", text.trim()); }}>
        <textarea aria-label={`${question.title}：自由回答`} placeholder={choices.length ? "或输入其他回答…" : "输入回答…"} value={text} onChange={(event) => setText(event.target.value)} />
        <button type="submit" disabled={!text.trim() || busy}>提交回答</button>
      </form>}
      <button type="button" className="human-dismiss" onClick={() => void answer("dismissed")}>忽略 / 取消</button>
    </fieldset>}
    {busy && <div className="human-status" role="status">提交中…</div>}
    {question.reason && <p className="human-status">{question.reason}</p>}
    {error && <div role="alert">{error}</div>}
  </div>;
}
