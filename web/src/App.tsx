import { ActorKind, isOperatorKind, operatorRole } from "./actor";
import type { MessageContent } from "./content";
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  createConversation,
  listActors,
  listConversations,
  submitMessage,
  type Actor,
  type Conversation,
  type Message,
} from "./api";
import { MessageBody, ReplyReference } from "./MessageBody";
import { MessagePoller } from "./message-poll";
import { mergeMessage, applyMessageEvent, messageStatusLabel } from "./message";
import { DetailPanel, detailOrganizer, type DetailSelection } from "./DetailPanel";

import { useConversationStream } from "./streams";
const selectedActorKey = "loopd.selected-actor";
const selectedConversationKey = "loopd.selected-conversation";

export function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [actors, setActors] = useState<Actor[]>([]);
  const [selectedActorID, setSelectedActorID] = useState<string>();
  const [selectedConversationID, setSelectedConversationID] = useState<string>();
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedMessageID, setSelectedMessageID] = useState<string>();
  const [detailSelection, setDetailSelection] = useState<DetailSelection>();
  const [submitting, setSubmitting] = useState(false);
  const [draft, setDraft] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const messagePoller = useRef<{ conversationID: string; poller: MessagePoller } | undefined>(undefined);
  useConversationStream(selectedConversationID, (delivery) => {
    if (delivery.event.stream_id) setMessages((current) => applyMessageEvent(current, delivery));
  }, (signal) => refreshMessages(selectedConversationID!, signal, true));

  const selectedConversation = conversations.find((item) => item.id === selectedConversationID);
  const selectedActor = actors.find((actor) => actorIdentity(actor) === selectedActorID);

  useEffect(() => {
    const controller = new AbortController();
    void listConversations(controller.signal)
      .then((items) => {
        setConversations(items);
        const saved = localStorage.getItem(selectedConversationKey);
        const selected = items.find((item) => item.id === saved)?.id ?? items[0]?.id;
        setSelectedConversationID(selected);
      })
      .catch((cause: unknown) => {
        if (!isAbort(cause)) setError(errorMessage(cause));
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const refresh = () => {
      void listActors(controller.signal)
        .then((discovered) => {
          const items = discovered.filter((actor) => actor.kind !== ActorKind.User);
          setActors(items);
          setSelectedActorID((current) => {
            const saved = current ?? localStorage.getItem(selectedActorKey) ?? undefined;
            const next = items.find((actor) => actorIdentity(actor) === saved) ?? items[0];
            if (next) localStorage.setItem(selectedActorKey, actorIdentity(next));
            else localStorage.removeItem(selectedActorKey);
            return next ? actorIdentity(next) : undefined;
          });
        })
        .catch((cause: unknown) => {
          if (!isAbort(cause)) setError(errorMessage(cause));
        });
    };
    refresh();
    const timer = window.setInterval(refresh, 10_000);
    return () => {
      window.clearInterval(timer);
      controller.abort();
    };
  }, []);

  useEffect(() => {
    if (!selectedConversationID) {
      setMessages([]);
      setSelectedMessageID(undefined);
      return;
    }
    localStorage.setItem(selectedConversationKey, selectedConversationID);
    const controller = new AbortController();
    void refreshMessages(selectedConversationID, controller.signal).then((items) => {
      if (controller.signal.aborted) return;
      const lastMessage = items.at(-1);
      setSelectedMessageID((current) => current ?? lastMessage?.id);
      if (lastMessage) setDetailSelection((current) => current ?? {
        parentID: selectedConversationID, organizer: detailOrganizer(lastMessage),
      });
    });
    return () => controller.abort();
  }, [selectedConversationID]);


  async function refreshMessages(conversationID: string, signal?: AbortSignal, sync = false): Promise<Message[]> {
    try {
      if (messagePoller.current?.conversationID !== conversationID) {
        messagePoller.current = { conversationID, poller: new MessagePoller(conversationID) };
      }
      const poller = messagePoller.current.poller;
      const items = await (sync ? poller.sync(signal) : poller.poll(signal));
      if (signal?.aborted) return [];
      setMessages((current) => {
        // Equal message revisions may carry refreshed reference previews/cards.
        let result = current.filter((m) => m.conversation_id === conversationID && !m.id.startsWith("local-"));
        for (const m of items) result = mergeMessage(result, m);
        return result;
      });
      return items;
    } catch (cause) {
      if (!isAbort(cause)) setError(errorMessage(cause));
      return [];
    }
  }

  function selectConversation(conversationID: string) {
    if (conversationID === selectedConversationID) return;
    messagePoller.current = undefined;
    setMessages([]);
    setSelectedMessageID(undefined);
    setDetailSelection(undefined);
    setSelectedConversationID(conversationID);
    setError(undefined);
  }

  function startConversation() {
    messagePoller.current = undefined;
    setSelectedConversationID(undefined);
    setMessages([]);
    setSelectedMessageID(undefined);
    setDetailSelection(undefined);
    setError(undefined);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text || !selectedActor || submitting) return;
    setSubmitting(true);
    setDraft("");
    setError(undefined);

    let conversationID = selectedConversationID;
    if (!conversationID) {
      try {
        const conversation = await createConversation(conversationName(text));
        conversationID = conversation.id;
        setConversations((current) => [conversation, ...current]);
        setSelectedConversationID(conversation.id);
        localStorage.setItem(selectedConversationKey, conversation.id);
      } catch (cause) {
        setError(errorMessage(cause));
        setDraft(text);
        setSubmitting(false);
        return;
      }
    }

    // Observe the recipient immediately, even before it publishes a main answer
    // or creates a workspace. The composer choice is not the detail selection.
    setSelectedMessageID(undefined);
    setDetailSelection({ parentID: conversationID, organizer: detailOrganizer({
      source_kind: "user", source_key: "web-user", target_kind: selectedActor.kind, target_key: selectedActor.key,
    }) });
    setMessages((current) => [
      ...current,
      {
        id: `local-${Date.now()}`,
        status: "completed",
        conversation_id: conversationID,
        task_id: "",
        source_kind: ActorKind.User,
        source_key: "web-user",
        target_kind: selectedActor.kind,
        target_key: selectedActor.key,
        content: textModel(text),
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
    ]);
    try {
      await submitMessage({
        conversationID, text, target: selectedActor,
        onTaskID: () => setSubmitting(false),
        onEvent: (delivery) => {
          if (!delivery.event.stream_id) return;
          setMessages((current) => applyMessageEvent(current.filter((m) => !m.id.startsWith("local-")), delivery));
        },
      });
    } catch (cause) {
      if (!isAbort(cause)) setError(errorMessage(cause));
    } finally {
      setSubmitting(false);
    }
  }

  const renderedMessages = messages;

  return (
    <div className="shell">
      <aside className="conversation-panel">
        <div className="brand">
          <div className="brand-mark">L</div>
          <div>
            <strong>loopd</strong>
            <span>Human · Operator · Harness</span>
          </div>
        </div>
        <button className="new-conversation" type="button" onClick={startConversation}>
          <span>＋</span> 新对话
        </button>
        <div className="panel-label">CONVERSATIONS</div>
        <nav className="conversation-list" aria-label="Conversation list">
          {loading && <div className="muted-state">加载中…</div>}
          {!loading && conversations.length === 0 && <div className="muted-state">还没有对话</div>}
          {conversations.map((conversation) => (
            <button
              className={conversation.id === selectedConversationID ? "conversation active" : "conversation"}
              key={conversation.id}
              type="button"
              onClick={() => selectConversation(conversation.id)}
            >
              <span className="conversation-title">{conversation.name || "Untitled conversation"}</span>
              <span className="conversation-time">{relativeTime(conversation.updated_at)}</span>
            </button>
          ))}
        </nav>
      </aside>

      <main className="chat-panel">
        <header className="chat-header">
          <div>
            <span className="eyebrow">CONVERSATION</span>
            <h1>{selectedConversation?.name || "新对话"}</h1>
          </div>
        </header>

        <section className="messages" aria-live="polite">
          {renderedMessages.length === 0 && (
            <div className="welcome">
              <div className="welcome-symbol">↻</div>
              <h2>从一个问题开始</h2>
              <p>{selectedActor?.description || "选择一个可用的 Operator 或 Harness，然后开始对话。"}</p>
            </div>
          )}
          {renderedMessages.map((message) => {
            const isLive = message.status === "streaming";
            const active = selectedMessageID === message.id;
            return (
              <article
                className={`message ${isOperatorKind(message.source_kind) ? ActorKind.Operator : message.source_kind} ${active ? "selected" : ""}`}
                key={message.id}
                id={`message-${message.id}`}
                onClick={() => {
                  setSelectedMessageID(message.id);
                  setDetailSelection({ parentID: message.conversation_id, organizer: detailOrganizer(message) });
                }}
              >
                <div className="message-author">
                  <span title={`${message.source_kind} / ${message.source_key}`}>{message.source_kind === ActorKind.User ? "YOU" : operatorRole(message.source_kind) ? operatorRole(message.source_kind)!.toUpperCase() : message.source_key.toUpperCase()}</span>
                  {message.status !== "completed" && <span className="run-badge">{messageStatusLabel(message.status)}</span>}
                </div>
                <div className="bubble">
                  <ReplyReference message={message} />
                  <MessageBody message={message} onReply={(result) => setMessages((current) => {
                    let next = mergeMessage(current, result.message);
                    if (result.reply) next = mergeMessage(next, result.reply);
                    return next;
                  })} empty={isLive ? <Typing /> : <span className="quiet">等待处理…</span>} />
                </div>
              </article>
            );
          })}
          {error && <div className="error-banner">{error}</div>}
        </section>

        <form className="composer" onSubmit={submit}>
          <textarea
            aria-label="Message"
            placeholder={selectedActor ? `给 ${actorName(selectedActor)} 发一个问题…` : "当前没有可用的 Actor"}
            rows={1}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
          />
          <button
            className="send-button"
            disabled={!draft.trim() || !selectedActor || submitting}
            type="submit"
            aria-label="Send"
          >
            ↑
          </button>
          <div className="composer-meta">
            <label className="actor-picker">
              <span className="actor-dot" />
              <span>发送给</span>
              <select
                aria-label="选择 Actor"
                disabled={actors.length === 0}
                value={selectedActorID ?? ""}
                onChange={(event) => {
                  setSelectedActorID(event.target.value);
                  localStorage.setItem(selectedActorKey, event.target.value);
                }}
              >
                {actors.length === 0 && <option value="">暂无可用 Actor</option>}
                {actors.filter((actor) => actor.kind === ActorKind.Operator).length > 0 && (
                  <optgroup label="Operators">
                    {actors.filter((actor) => actor.kind === ActorKind.Operator).map((actor) => (
                      <option key={actorIdentity(actor)} value={actorIdentity(actor)}>{actorLabel(actor)}</option>
                    ))}
                  </optgroup>
                )}
                {actors.filter((actor) => actor.kind === ActorKind.Harness).length > 0 && (
                  <optgroup label="Harnesses">
                    {actors.filter((actor) => actor.kind === ActorKind.Harness).map((actor) => (
                      <option key={actorIdentity(actor)} value={actorIdentity(actor)}>{actorLabel(actor)}</option>
                    ))}
                  </optgroup>
                )}
                {actors.filter((actor) => actor.kind !== ActorKind.Operator && actor.kind !== ActorKind.Harness).length > 0 && (
                  <optgroup label="Actors">
                    {actors.filter((actor) => actor.kind !== ActorKind.Operator && actor.kind !== ActorKind.Harness).map((actor) => (
                      <option key={actorIdentity(actor)} value={actorIdentity(actor)}>{actorLabel(actor)}</option>
                    ))}
                  </optgroup>
                )}
              </select>
            </label>
            <span>Enter 发送 · Shift + Enter 换行</span>
          </div>
        </form>
      </main>

      <DetailPanel
        onReply={(result) => setMessages((current) => {
          let next = current;
          for (const message of [result.message, result.reply]) {
            if (message && message.conversation_id === selectedConversationID) next = mergeMessage(next, message);
          }
          return next;
        })}
        selection={detailSelection?.parentID === selectedConversationID ? detailSelection : undefined}
      />
    </div>
  );
}

function Typing() {
  return <span className="typing"><i /><i /><i /></span>;
}

function textModel(text: string): MessageContent {
  return {
    version: "1.1", biz: "chat", meta: {},
    blocks: [{ id: "question", type: "text", role: "user", content: text }],
  };
}

function conversationName(text: string): string {
  const compact = text.replace(/\s+/g, " ").trim();
  return compact.length > 32 ? `${compact.slice(0, 32)}…` : compact;
}

function actorIdentity(actor: Pick<Actor, "kind" | "key">): string {
  return `${actor.kind}:${actor.key}`;
}

function actorName(actor: Actor): string {
  return actor.display_name || actor.key;
}

function actorLabel(actor: Actor): string {
  const kind = actor.kind === ActorKind.Operator ? "Operator" : actor.kind === ActorKind.Harness ? "Harness" : actor.kind;
  return `${kind} · ${actorName(actor)}`;
}

function relativeTime(value: string): string {
  const elapsed = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(elapsed) || elapsed < 60_000) return "刚刚";
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  return new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric" }).format(new Date(value));
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

function isAbort(cause: unknown): boolean {
  return cause instanceof DOMException && cause.name === "AbortError";
}
