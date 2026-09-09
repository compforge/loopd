import { useLayoutEffect, useRef } from "react";
import { ActorKind, isOperatorKind, operatorRole } from "../actors/model";
import type { HumanResult, Message } from "./types";
import { messageStatusLabel } from "./state";
import { LazyMessage } from "./LazyMessage";
import { MessageBody } from "./MessageBody";
import { ReplyReference } from "./ReplyReference";

export function MessageList({
  messages,
  selectedMessageID,
  onSelect,
  onLoad,
  onReply,
  loadOlder,
  hasOlder,
  loadingOlder,
  error,
  pendingText,
  description,
}: {
  messages: Message[];
  selectedMessageID?: string;
  onSelect(message: Message): void;
  onLoad(message: Message): void;
  onReply(result: HumanResult): void;
  loadOlder(beforeInsert?: () => void): Promise<void>;
  hasOlder: boolean;
  loadingOlder: boolean;
  error?: string;
  pendingText?: string;
  description?: string;
}) {
  const container = useRef<HTMLElement>(null);
  const anchor = useRef<{ id: string; top: number } | undefined>(undefined);
  const initialScroll = useRef(true);
  useLayoutEffect(() => {
    if (!container.current) return;
    if (anchor.current) {
      const element = document.getElementById(`message-${anchor.current.id}`);
      if (element) container.current.scrollTop += element.getBoundingClientRect().top - anchor.current.top;
      anchor.current = undefined;
    } else if (initialScroll.current && messages.length) {
      container.current.scrollTop = container.current.scrollHeight;
      initialScroll.current = false;
    }
  }, [messages]);
  const older = () =>
    loadOlder(() => {
      const first = messages[0];
      const element = first && document.getElementById(`message-${first.id}`);
      if (element) anchor.current = { id: first.id, top: element.getBoundingClientRect().top };
    });
  return (
    <section className="messages" ref={container} aria-live="polite">
      {hasOlder && (
        <button className="load-history" type="button" disabled={loadingOlder} onClick={() => void older()}>
          {loadingOlder ? "加载中…" : "加载更早的消息"}
        </button>
      )}
      {messages.length === 0 && !pendingText && <Welcome description={description} />}
      {messages.map((message) => (
        <LazyMessage key={message.id} message={message} onLoad={onLoad}>
          <article
            className={`message ${isOperatorKind(message.source_kind) ? ActorKind.Operator : message.source_kind} ${selectedMessageID === message.id ? "selected" : ""}`}
            id={`message-${message.id}`}
            onClick={() => onSelect(message)}
          >
            <div className="message-author">
              <span title={`${message.source_kind} / ${message.source_key}`}>
                {message.source_kind === ActorKind.User
                  ? "YOU"
                  : (operatorRole(message.source_kind) || message.source_key).toUpperCase()}
              </span>
              {message.status !== "completed" && (
                <span className="run-badge">{messageStatusLabel(message.status)}</span>
              )}
            </div>
            <div className="bubble">
              <ReplyReference message={message} onLoad={onLoad} />
              <MessageBody
                message={message}
                onReply={onReply}
                empty={
                  message.status === "streaming" ? (
                    <span className="typing">
                      <i />
                      <i />
                      <i />
                    </span>
                  ) : (
                    <span className="quiet">等待处理…</span>
                  )
                }
              />
            </div>
          </article>
        </LazyMessage>
      ))}
      {pendingText && <PendingMessage text={pendingText} />}
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
    </section>
  );
}
export function Welcome({ description }: { description?: string }) {
  return (
    <div className="welcome">
      <div className="welcome-symbol">↻</div>
      <h2>从一个问题开始</h2>
      <p>{description || "选择一个可用的 Operator 或 Harness，然后开始对话。"}</p>
    </div>
  );
}
// Pending submissions are local UI state, never fabricated persisted Messages.
export function PendingMessage({ text }: { text: string }) {
  return (
    <article className="message user">
      <div className="message-author">
        <span>YOU</span>
        <span className="run-badge">发送中…</span>
      </div>
      <div className="bubble">
        <p>{text}</p>
      </div>
    </article>
  );
}
