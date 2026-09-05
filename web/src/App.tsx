import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  parseUIModel,
  PatchOp,
  type UIModel,
} from "@compforge/agentue/ui";
import {
  createConversation,
  listActors,
  listConversations,
  listMessages,
  streamMessage,
  type Actor,
  type Conversation,
  type Message,
} from "./api";
import { HumanMessage } from "./HumanMessage";
import { mergeMessage, applyMessageEvent } from "./message";
import { DetailPanel } from "./DetailPanel";

import { readSubscriptions, writeSubscription, type StoredSubscription } from "./streams";
const selectedActorKey = "loopd.selected-actor";
const selectedConversationKey = "loopd.selected-conversation";

type ConnectionStatus = "connecting" | "connected" | "reconnecting" | "failed";

interface LiveSubscription {
  messages?: Message[];
  conversationID: string;
  taskID: string;
  lastEventID: string;
  status: ConnectionStatus;
}

export function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [actors, setActors] = useState<Actor[]>([]);
  const [selectedActorID, setSelectedActorID] = useState<string>();
  const [selectedConversationID, setSelectedConversationID] = useState<string>();
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedMessageID, setSelectedMessageID] = useState<string>();
  const [liveSubscriptions, setLiveSubscriptions] = useState<Record<string, LiveSubscription>>({});
  const [submitting, setSubmitting] = useState(false);
  const [draft, setDraft] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const streams = useRef(new Map<string, AbortController>());
  useEffect(() => () => { for (const controller of streams.current.values()) controller.abort(); }, []);

  const selectedConversation = conversations.find((item) => item.id === selectedConversationID);
  const selectedActor = actors.find((actor) => actorIdentity(actor) === selectedActorID);
  const selectedMessage = messages.find((message) => message.id === selectedMessageID);
  const subscription = selectedConversationID ? liveSubscriptions[selectedConversationID] : undefined;

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
        .then((items) => {
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
      const lastResponse = items.findLast((message) => message.kind !== "user");
      setSelectedMessageID((current) => current ?? lastResponse?.id);
      const stored = readSubscriptions()[selectedConversationID];
      const input = items.findLast((message) => message.purpose === "input" && message.task_id);
      if (!streams.current.has(selectedConversationID) && (stored || input)) {
        void observeConversation(selectedConversationID, stored ?? { taskID: input!.task_id, lastEventID: "" });
      }
    });
    return () => controller.abort();
  }, [selectedConversationID]);

  useEffect(() => {
    if (!selectedConversationID) return;
    const controller = new AbortController();
    // Actors may publish without an active user Chat; discover their snapshots too.
    const timer = window.setInterval(() => { void refreshMessages(selectedConversationID, controller.signal); }, 2000);
    return () => { window.clearInterval(timer); controller.abort(); };
  }, [selectedConversationID]);

  async function refreshMessages(conversationID: string, signal?: AbortSignal): Promise<Message[]> {
    try {
      const items = await listMessages(conversationID, signal);
      if (signal?.aborted) return [];
      setMessages((current) => {
        let result = items;
        for (const m of current) if (m.conversation_id === conversationID && !m.id.startsWith("local-")) result = mergeMessage(result, m);
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
    for (const controller of streams.current.values()) controller.abort();
    streams.current.clear();
    setMessages([]);
    setLiveSubscriptions({});
    setSelectedMessageID(undefined);
    setSelectedConversationID(conversationID);
    setError(undefined);
  }

  function startConversation() {
    for (const controller of streams.current.values()) controller.abort();
    streams.current.clear();
    setSelectedConversationID(undefined);
    setMessages([]);
    setLiveSubscriptions({});
    setSelectedMessageID(undefined);
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

    setMessages((current) => [
      ...current,
      {
        id: `local-${Date.now()}`,
        conversation_id: conversationID,
        task_id: "",
        kind: "user",
        key: "web-user",
        content: textModel(text),
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
    ]);
    await observeConversation(conversationID, undefined, text, selectedActor);
  }

  async function observeConversation(
    conversationID: string,
    stored?: StoredSubscription,
    text?: string,
    requestedTarget?: Pick<Actor, "kind" | "key">,
  ) {
    const slot = conversationID;
    if (stored && streams.current.has(slot)) return;
    streams.current.get(slot)?.abort();
    const controller = new AbortController();
    streams.current.set(slot, controller);
    let taskID = stored?.taskID ?? "";
    let lastEventID = stored?.lastEventID ?? "";
    let awaitingID = text !== undefined;
    let liveMessages: Message[] = [];
    const target = requestedTarget;
    const update = (status: ConnectionStatus) => {
      if (controller.signal.aborted) return;
      setLiveSubscriptions((current) => ({ ...current, [slot]: {
        conversationID, taskID, lastEventID, status,
        messages: [...liveMessages],
      } }));
    };
    update("connecting");
    try {
      for (;;) {
        try {
          await streamMessage({
            conversationID, taskID: taskID || undefined, lastEventID: lastEventID || undefined,
            text, target, signal: controller.signal,
            onTaskID: (value) => {
              if (controller.signal.aborted) return;
              const first = !taskID;
              taskID = value;
              if (awaitingID) { setSubmitting(false); awaitingID = false; }
              if (first) void refreshMessages(conversationID, controller.signal);
              writeSubscription(conversationID, { taskID, lastEventID });
              update("connected");
            },
            onEvent: (delivery) => {
              if (controller.signal.aborted) return;
              const { event: patch, eventId, messageID, message } = delivery;
              if (messageID && message) {
                liveMessages = applyMessageEvent(liveMessages, delivery);
                if (message.conversation_id === conversationID) {
                  const updated = liveMessages.find((item) => item.id === messageID)!;
                  setMessages((current) => mergeMessage(current, updated));
                  if (message.kind !== "user") {
                    setSelectedMessageID((current) => current ?? messageID);
                  }
                }
                // A Message's END closes only that Message.
                update("connected");
                return;
              }
              // Control events carry transport lifecycle only, never a bubble.
              if (eventId) lastEventID = eventId;
              if (patch.op === PatchOp.ERROR) setError(String(patch.meta.error.message ?? "连接错误"));
              writeSubscription(conversationID, { taskID, lastEventID });
              update("connected");
            },
          });
          if (!taskID) throw new Error("chat stream closed before an ID was returned");
        } catch (cause) {
          if (isAbort(cause)) return;
          if (!taskID) throw cause;
        }
        update("reconnecting");
        await delay(1_500, controller.signal);
      }
    } catch (cause) {
      if (!isAbort(cause)) { setError(errorMessage(cause)); update("failed"); }
    } finally {
      if (streams.current.get(slot) === controller) streams.current.delete(slot);
      if (awaitingID) setSubmitting(false);
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
            const model = safeModel(message.content);
            const output = model?.meta.output;
            const isLive = message.purpose === "output" && output !== null && typeof output === "object" && "ended" in output && output.ended === false;
            const text = messageText(model);
            const active = selectedMessageID === message.id;
            return (
              <article
                className={`message ${message.kind} ${active ? "selected" : ""}`}
                key={message.id}
                id={`message-${message.id}`}
                onClick={() => setSelectedMessageID(message.id)}
              >
                <div className="message-author">
                  <span>{message.kind === "user" ? "YOU" : message.key.toUpperCase()}</span>
                  {isLive && <span className="run-badge running">STREAMING</span>}
                </div>
                <div className="bubble">
                  {message.reply_to_id && <a className="reply-reference" href={`#message-${message.reply_to_id}`}>查看所回复的消息</a>}
                  {message.purpose === "human_request" || message.purpose === "human_reply" ? (
                    <HumanMessage message={message} replyTo={renderedMessages.find((item) => item.id === message.reply_to_id)} onReply={(result) => setMessages((current) => {
                      let next = mergeMessage(current, result.message);
                      if (result.reply) next = mergeMessage(next, result.reply);
                      return next;
                    })} />
                  ) : text || (isLive ? <Typing /> : <span className="quiet">等待处理…</span>)}
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
                {actors.filter((actor) => actor.kind === "operator").length > 0 && (
                  <optgroup label="Operators">
                    {actors.filter((actor) => actor.kind === "operator").map((actor) => (
                      <option key={actorIdentity(actor)} value={actorIdentity(actor)}>{actorLabel(actor)}</option>
                    ))}
                  </optgroup>
                )}
                {actors.filter((actor) => actor.kind === "harness").length > 0 && (
                  <optgroup label="Harnesses">
                    {actors.filter((actor) => actor.kind === "harness").map((actor) => (
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
        message={selectedMessage}
        liveMessages={subscription?.messages}
        running={subscription?.status === "connected"}
      />
    </div>
  );
}

function Typing() {
  return <span className="typing"><i /><i /><i /></span>;
}

function safeModel(value: unknown): UIModel | undefined {
  try {
    return parseUIModel(value);
  } catch {
    return undefined;
  }
}

function messageText(model: UIModel | undefined): string {
  if (!model) return "";
  const answer = model.blocks.find((block) => block.id === "answer" && block.type === "text");
  if (answer && typeof answer.content === "string") return answer.content;
  return model.blocks
    .filter((block) => block.type === "text" && typeof block.content === "string")
    .map((block) => block.content as string)
    .join("\n");
}

function textModel(text: string): UIModel {
  return {
    version: "1.0", biz: "chat", meta: {},
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
  const kind = actor.kind === "operator" ? "Operator" : "Harness";
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

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException("aborted", "AbortError")); return; }
    const abort = () => { window.clearTimeout(timer); reject(new DOMException("aborted", "AbortError")); };
    const timer = window.setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, milliseconds);
    signal.addEventListener("abort", abort, { once: true });
  });
}
