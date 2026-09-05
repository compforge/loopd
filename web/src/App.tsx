import { useEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent } from "react";
import {
  applyPatch,
  parseUIModel,
  PatchOp,
  type BaseBlock,
  type Snapshot,
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
import { traceCallID, traceColor, traceLabel } from "./trace";

const activeTasksKey = "loopd.active-tasks";
const selectedActorKey = "loopd.selected-actor";
const selectedConversationKey = "loopd.selected-conversation";
const legacyRouter: Pick<Actor, "kind" | "key"> = { kind: "operator", key: "router" };

type RunStatus = "connecting" | "running" | "reconnecting" | "completed" | "failed";

interface LiveTask {
  conversationID: string;
  taskID: string;
  lastEventID: string;
  snapshot: Snapshot;
  status: RunStatus;
  target: Pick<Actor, "kind" | "key">;
}

interface StoredTask {
  taskID: string;
  lastEventID: string;
  target?: Pick<Actor, "kind" | "key">;
}

export function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [actors, setActors] = useState<Actor[]>([]);
  const [selectedActorID, setSelectedActorID] = useState<string>();
  const [selectedConversationID, setSelectedConversationID] = useState<string>();
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedTaskID, setSelectedTaskID] = useState<string>();
  const [liveTask, setLiveTask] = useState<LiveTask>();
  const [draft, setDraft] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const streamAbort = useRef<AbortController | undefined>(undefined);
  const streamingConversation = useRef<string | undefined>(undefined);

  const selectedConversation = conversations.find((item) => item.id === selectedConversationID);
  const selectedActor = actors.find((actor) => actorIdentity(actor) === selectedActorID);
  const operatorMessages = messages.filter((message) => message.kind === "operator");
  const selectedOperatorMessage = operatorMessages.find((message) => message.task_id === selectedTaskID);
  const detailModel = useMemo(() => {
    if (liveTask?.target.kind === "operator" && liveTask.taskID === selectedTaskID) return toUIModel(liveTask.snapshot);
    return selectedOperatorMessage ? safeModel(selectedOperatorMessage.content) : undefined;
  }, [liveTask, selectedOperatorMessage, selectedTaskID]);

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
      setSelectedTaskID(undefined);
      return;
    }
    localStorage.setItem(selectedConversationKey, selectedConversationID);
    const controller = new AbortController();
    void refreshMessages(selectedConversationID, controller.signal).then((items) => {
      const lastOperator = items.findLast((message) => message.kind === "operator");
      setSelectedTaskID((current) => current ?? lastOperator?.task_id);
    });
    const active = readActiveTasks()[selectedConversationID];
    if (active && streamingConversation.current !== selectedConversationID) {
      void observeTask(selectedConversationID, active);
    }
    return () => controller.abort();
  }, [selectedConversationID]);

  async function refreshMessages(conversationID: string, signal?: AbortSignal): Promise<Message[]> {
    try {
      const items = await listMessages(conversationID, signal);
      setMessages(items);
      return items;
    } catch (cause) {
      if (!isAbort(cause)) setError(errorMessage(cause));
      return [];
    }
  }

  function selectConversation(conversationID: string) {
    streamAbort.current?.abort();
    streamingConversation.current = undefined;
    setMessages([]);
    setLiveTask(undefined);
    setSelectedTaskID(undefined);
    setSelectedConversationID(conversationID);
    setError(undefined);
  }

  function startConversation() {
    streamAbort.current?.abort();
    streamingConversation.current = undefined;
    setSelectedConversationID(undefined);
    setMessages([]);
    setLiveTask(undefined);
    setSelectedTaskID(undefined);
    setError(undefined);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text || !selectedActor || liveTask && !isTerminal(liveTask.status)) return;
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
    await observeTask(conversationID, undefined, text, selectedActor);
  }

  async function observeTask(
    conversationID: string,
    stored?: StoredTask,
    text?: string,
    requestedTarget?: Pick<Actor, "kind" | "key">,
  ) {
    streamAbort.current?.abort();
    const controller = new AbortController();
    streamAbort.current = controller;
    streamingConversation.current = conversationID;
    let taskID = stored?.taskID ?? "";
    let lastEventID = stored?.lastEventID ?? "";
    let snapshot: Snapshot = {};
    let failed = false;
    const target = requestedTarget ?? stored?.target ?? legacyRouter;

    setLiveTask({ conversationID, taskID, lastEventID, snapshot, status: "connecting", target });
    try {
      for (;;) {
        let ended = false;
        try {
          await streamMessage({
            conversationID,
            taskID: taskID || undefined,
            lastEventID: lastEventID || undefined,
            text,
            target,
            signal: controller.signal,
            onTaskID: (value) => {
              taskID = value;
              if (target.kind === "operator") setSelectedTaskID(value);
              writeActiveTask(conversationID, { taskID, lastEventID, target });
              setLiveTask({ conversationID, taskID, lastEventID, snapshot, status: "running", target });
            },
            onEvent: ({ event: patch, eventId }) => {
              snapshot = applyPatch(structuredClone(snapshot), patch);
              if (eventId) lastEventID = eventId;
              if (patch.op === PatchOp.ERROR) failed = true;
              if (patch.op === PatchOp.END) ended = true;
              writeActiveTask(conversationID, { taskID, lastEventID, target });
              setLiveTask({
                conversationID,
                taskID,
                lastEventID,
                snapshot: structuredClone(snapshot),
                status: ended ? (failed ? "failed" : "completed") : "running",
                target,
              });
            },
          });
          if (ended) break;
          if (!taskID) throw new Error("chat stream closed before a Task was created");
        } catch (cause) {
          if (isAbort(cause)) return;
          if (!taskID) throw cause;
          setLiveTask({
            conversationID,
            taskID,
            lastEventID,
            snapshot: structuredClone(snapshot),
            status: "reconnecting",
            target,
          });
          await delay(1_500, controller.signal);
        }
      }

      removeActiveTask(conversationID);
      const items = await refreshMessages(conversationID, controller.signal);
      const answer = items.find((message) => message.task_id === taskID && message.kind === target.kind);
      if (answer?.kind === "operator") setSelectedTaskID(answer.task_id);
      const refreshed = await listConversations(controller.signal);
      setConversations(refreshed);
    } catch (cause) {
      if (!isAbort(cause)) setError(errorMessage(cause));
    } finally {
      if (streamingConversation.current === conversationID) {
        streamingConversation.current = undefined;
      }
    }
  }

  const renderedMessages = useMemo(() => {
    const values = [...messages];
    if (liveTask?.taskID && !values.some((message) => message.task_id === liveTask.taskID && message.kind !== "user")) {
      values.push({
        id: `live-${liveTask.taskID}`,
        conversation_id: liveTask.conversationID,
        task_id: liveTask.taskID,
        kind: liveTask.target.kind,
        key: liveTask.target.key,
        content: toUIModel(liveTask.snapshot) ?? emptyModel(),
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      });
    }
    return values;
  }, [messages, liveTask]);

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
            const isLive = liveTask?.taskID === message.task_id && message.kind !== "user";
            const model = isLive ? toUIModel(liveTask.snapshot) : safeModel(message.content);
            const text = messageText(model, message.kind);
            const active = message.kind === "operator" && selectedTaskID === message.task_id;
            return (
              <article
                className={`message ${message.kind} ${active ? "selected" : ""}`}
                key={message.id}
                onClick={() => setSelectedTaskID(message.kind === "operator" ? message.task_id : undefined)}
              >
                <div className="message-author">
                  <span>{message.kind === "user" ? "YOU" : message.key.toUpperCase()}</span>
                  {isLive && liveTask && <RunBadge status={liveTask.status} />}
                </div>
                <div className="bubble">
                  {text || (isLive ? <Typing /> : <span className="quiet">等待处理…</span>)}
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
            disabled={!draft.trim() || !selectedActor || Boolean(liveTask && !isTerminal(liveTask.status))}
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
                disabled={Boolean(liveTask && !isTerminal(liveTask.status)) || actors.length === 0}
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

      <aside className="detail-panel">
        <header className="detail-header">
          <span className="eyebrow">OPERATOR TRACE</span>
          <h2>处理详情</h2>
        </header>
        {!selectedTaskID || !detailModel ? (
          <div className="detail-empty">
            <div>◎</div>
            <p>选择一条 Operator 消息，查看它如何调用 Harness 完成任务。</p>
          </div>
        ) : (
          <DetailView
            model={detailModel}
            taskID={selectedTaskID}
            status={liveTask?.taskID === selectedTaskID ? liveTask.status : "completed"}
          />
        )}
      </aside>
    </div>
  );
}

function DetailView({ model, taskID, status }: { model: UIModel; taskID: string; status: RunStatus }) {
  const details = model.blocks.filter((block) => block.id !== "answer");
  const error = model.meta.error;
  return (
    <div className="detail-content">
      <div className="task-summary">
        <div>
          <small>TASK</small>
          <code>{shortID(taskID)}</code>
        </div>
        <RunBadge status={error ? "failed" : status} />
      </div>
      <div className="timeline">
        {details.length === 0 && !error && <div className="muted-state">等待 Operator 产生处理事件…</div>}
        {details.map((block, index) => (
          <BlockCard block={block} index={index} key={block.id} />
        ))}
        {error && (
          <div className="detail-card failed">
            <div className="detail-card-title">执行失败</div>
            <p>{error.message}</p>
          </div>
        )}
      </div>
    </div>
  );
}

function BlockCard({ block, index }: { block: BaseBlock; index: number }) {
  const isTool = block.type === "tool";
  const content = typeof block.content === "string" ? block.content : undefined;
  const callID = traceCallID(block);
  const effectKey = typeof block.effect_key === "string" ? block.effect_key : undefined;
  const style = callID ? { "--harness-color": traceColor(callID) } as CSSProperties : undefined;
  return (
    <div className={`detail-card${callID ? " harness-trace" : ""}`} style={style}>
      <div className="timeline-node">{index + 1}</div>
      <div className="detail-card-head">
        <span className={`block-kind ${isTool ? "tool" : "harness"}`}>{isTool ? "TOOL" : "HARNESS"}</span>
        {typeof block.status === "string" && <span className="block-status">{block.status}</span>}
      </div>
      <div className="detail-card-title">
        {traceLabel(block, index)}
      </div>
      {effectKey && isTool && typeof block.name === "string" && <div className="detail-card-subtitle">{block.name}</div>}
      {content && <p>{content}</p>}
    </div>
  );
}

function RunBadge({ status }: { status: RunStatus }) {
  const labels: Record<RunStatus, string> = {
    connecting: "CONNECTING",
    running: "RUNNING",
    reconnecting: "RECONNECTING",
    completed: "COMPLETED",
    failed: "FAILED",
  };
  return <span className={`run-badge ${status}`}>{labels[status]}</span>;
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

function toUIModel(snapshot: Snapshot): UIModel | undefined {
  return safeModel(snapshot);
}

function messageText(model: UIModel | undefined, kind: Message["kind"]): string {
  if (!model) return "";
  const answer = model.blocks.find((block) => block.id === "answer" && block.type === "text");
  if (answer && typeof answer.content === "string") return answer.content;
  // An Operator response owns both its final answer and its internal Harness
  // progress. Keep progress in the detail pane so the main conversation does
  // not briefly expose implementation output while the answer is streaming.
  if (kind === "operator") return model.meta.error?.message ?? "";
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

function emptyModel(): UIModel {
  return { version: "1.0", biz: "chat", meta: {}, blocks: [] };
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

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function relativeTime(value: string): string {
  const elapsed = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(elapsed) || elapsed < 60_000) return "刚刚";
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  return new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric" }).format(new Date(value));
}

function isTerminal(status: RunStatus): boolean {
  return status === "completed" || status === "failed";
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

function isAbort(cause: unknown): boolean {
  return cause instanceof DOMException && cause.name === "AbortError";
}

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, milliseconds);
    signal.addEventListener("abort", () => {
      window.clearTimeout(timer);
      reject(new DOMException("aborted", "AbortError"));
    }, { once: true });
  });
}

function readActiveTasks(): Record<string, StoredTask> {
  try {
    return JSON.parse(localStorage.getItem(activeTasksKey) ?? "{}") as Record<string, StoredTask>;
  } catch {
    return {};
  }
}

function writeActiveTask(conversationID: string, task: StoredTask) {
  const tasks = readActiveTasks();
  tasks[conversationID] = task;
  localStorage.setItem(activeTasksKey, JSON.stringify(tasks));
}

function removeActiveTask(conversationID: string) {
  const tasks = readActiveTasks();
  delete tasks[conversationID];
  localStorage.setItem(activeTasksKey, JSON.stringify(tasks));
}
