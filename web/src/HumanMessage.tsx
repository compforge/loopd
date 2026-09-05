import { useState } from "react";
import { replyHuman, type HumanResult, type Message } from "./api";
import { humanStatus, type HumanQuestion } from "./human";

export function HumanMessage({ message, onReply, replyTo }: { message: Message; replyTo?: Message; onReply(result: HumanResult): void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const block = message.content.blocks.find((b) => b.type === "ask" || b.type === "confirm" || b.type === "human_reply");
  if (!block) return null;
  if (block.type === "human_reply") {
    const original = replyTo?.id === message.reply_to_message_id ? replyTo?.content.blocks.find((b) => b.type === "ask" || b.type === "confirm") : undefined;
    const value = String(block.value ?? "");
    const label = original?.type === "confirm" ? (value === "accepted" ? "已同意" : value === "declined" ? "已拒绝" : value) : value;
    return <span>{block.outcome === "dismissed" ? "已忽略" : label}</span>;
  }
  const question = block as unknown as HumanQuestion;
  const pending = question.status === "pending";
  async function answer(outcome: "success" | "dismissed", value?: string) {
    if (busy || !pending) return;
    setBusy(true); setError(undefined);
    try { onReply(await replyHuman(message, { reply_to_message_id: message.id, outcome, value })); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setBusy(false); }
  }
  return <div className="human-question" onKeyDown={(event) => {
    if (event.key === "Escape" && pending) { event.preventDefault(); event.stopPropagation(); void answer("dismissed"); }
  }}>
    <strong>{question.title}</strong>
    <p>{question.prompt}</p>
    <div className="human-status">{humanStatus(question.status)} · 截止 {new Date(question.deadline).toLocaleString()}</div>
    {pending && <fieldset disabled={busy}>
      {question.type === "confirm" ? <div className="human-actions">
        <button type="button" onClick={() => void answer("success", "accepted")}>{question.confirm_label || "同意"}</button>
        <button type="button" onClick={() => void answer("success", "declined")}>{question.decline_label || "拒绝"}</button>
      </div> : <>
        <div className="human-choices">{question.choices?.map((choice) => <button type="button" key={choice.value} onClick={() => void answer("success", choice.value)}>
          <span>{choice.label}</span>{choice.description && <small>{choice.description}</small>}
        </button>)}</div>
        {question.allow_other && <form onSubmit={(event) => { event.preventDefault(); if (text.trim()) void answer("success", text.trim()); }}>
          <textarea aria-label={question.title} placeholder={question.choices?.length ? "或输入其他回答…" : "输入回答…"} value={text} onChange={(event) => setText(event.target.value)} />
          <button type="submit" disabled={!text.trim() || busy}>提交回答</button>
        </form>}
      </>}
      <button className="human-dismiss" type="button" onClick={() => void answer("dismissed")}>忽略 / 取消</button>
    </fieldset>}
    {error && <div role="alert">{error}</div>}
  </div>;
}
