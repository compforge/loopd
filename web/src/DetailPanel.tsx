import { ActorKind, operatorOwner } from "./actor";
import { MessageBody, ReplyReference } from "./MessageBody";
import type { HumanResult } from "./api";
import { messageStatusLabel, mergeMessage } from "./message";
import { useConversationStream } from "./streams";
import { applyMessageEvent } from "./message";
import { MessagePoller } from "./message-poll";
import { useEffect, useRef, useState, type CSSProperties } from "react";
import { parseMessageContent, type MessageContent } from "./content";
import { findDetailConversation, type Conversation, type Message } from "./api";
import { traceColor, traceLabel } from "./trace";
import { groupParallelMessages } from "./parallel";

interface Detail {
  scope: string;
  conversation?: Conversation;
  messages: Message[];
  error?: string;
}

export interface DetailSelection {
  parentID: string;
  organizer?: { kind: typeof ActorKind.Operator; key: string };
}

/** @spec 按父会话/Operator 观察工作会话，不等待主回答；切换参与者不能泄漏上一个查询的结果。 */
export function DetailPanel({ selection, onReply }: {
  selection?: DetailSelection;
  onReply?(result: HumanResult): void;
}) {
  const history = useRef<{ scope: string; poller: MessagePoller } | undefined>(undefined);
  const [detail, setDetail] = useState<Detail>();
  const parentID = selection?.parentID;
  const organizer = selection?.organizer;
  const actorKind = organizer?.kind;
  const actorKey = organizer?.key;
  const scope = JSON.stringify([parentID, actorKind, actorKey]);
  useEffect(() => {
    if (!parentID || !actorKind || !actorKey) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let conversation: Conversation | undefined;
    let poller: MessagePoller | undefined;
    let messages: Message[] = [];
    let loaded = false;
    // The actor's workspace stays observable beyond any single UI delivery.
    async function refresh() {
      try {
        conversation ??= await findDetailConversation(parentID!, actorKind!, actorKey!, controller.signal);
        if (conversation) {
          poller ??= new MessagePoller(conversation.id);
          history.current = { scope, poller };
          for (const message of await poller.poll(controller.signal)) messages = mergeMessage(messages, message);
          loaded = true;
        }
        if (!controller.signal.aborted) setDetail((current) => {
          let merged = current?.scope === scope ? current.messages : [];
          for (const message of messages) merged = mergeMessage(merged, message);
          return { scope, conversation, messages: merged };
        });
      } catch (cause) {
        if (!controller.signal.aborted) {
          setDetail({ scope, messages: [], error: String(cause) });
        }
      } finally {
        if (!controller.signal.aborted && !loaded) timer = setTimeout(refresh, 2_000);
      }
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [scope, parentID, actorKind, actorKey]);

  const selected = detail?.scope === scope ? detail : undefined;
  useConversationStream(selected?.conversation?.id, (event) => {
    if (!event.event.stream_id) return;
    setDetail((current) => current?.scope === scope
      ? { ...current, messages: applyMessageEvent(current.messages, event) } : current);
  }, async (signal) => {
    const source = history.current;
    if (source?.scope !== scope) return;
    try {
      const updates = await source.poller.sync(signal);
      if (signal.aborted) return;
      setDetail((current) => {
        if (current?.scope !== scope) return current;
        let messages = current.messages;
        for (const message of updates) messages = mergeMessage(messages, message);
        return { ...current, messages };
      });
    } catch (cause) {
      if (!signal.aborted) setDetail((current) => current?.scope === scope ? { ...current, error: String(cause) } : current);
    }
  });
  const visible = selected?.messages ?? [];
  const groups = groupParallelMessages(visible);
  const indices = new Map(visible.map((item, index) => [item.id, index]));
  return (
    <aside className="detail-panel">
      <header className="detail-header">
        <span className="eyebrow">CONVERSATION DETAIL</span>
        <h2>处理详情{actorKey ? ` · ${actorKey}` : ""}</h2>
      </header>
      {!organizer || !selected?.conversation ? (
        <div className="detail-empty">
          <div>◎</div>
          <p>{selected?.error ?? (!selection ? "发送消息或选择历史消息，查看相关 Operator 的工作会话。" : !organizer ? "这条消息未关联 Operator 工作会话。" : !selected ? `正在查找 ${actorKey} 的工作会话…` : `等待 ${actorKey} 创建工作会话…`)}</p>
        </div>
      ) : (
        <div className="detail-content" data-conversation-id={selected.conversation.id}>
          <div className="task-summary">
            <div><small>OPERATOR CONVERSATION</small><code>{selected.conversation.actor_key}</code></div>
          </div>
          <div className="timeline">
            {visible.length === 0 && <div className="muted-state">等待处理消息…</div>}
            {groups.map((group) => (
              <section className="detail-group" key={group.columns[0][0].id}>
                <div className="parallel-scroll">
                  <div className="parallel-columns" style={{ gridTemplateColumns: `repeat(${group.columns.length}, 240px)` }}>
                    {group.columns.map((column) => (
                      <div className="parallel-column" key={column[0].id}>
                        {column.map((item) => <DetailMessage key={item.id} message={item} index={indices.get(item.id)!} onReply={(result) => {
                          setDetail((current) => {
                            if (current?.scope !== scope) return current;
                            let messages = mergeMessage(current.messages, result.message);
                            if (result.reply) messages = mergeMessage(messages, result.reply);
                            return { ...current, messages };
                          });
                          onReply?.(result);
                        }} />)}
                      </div>
                    ))}
                  </div>
                </div>
              </section>
            ))}
          </div>
        </div>
      )}
    </aside>
  );
}

export function DetailMessage({ message, index, onReply }: { message: Message; index: number; onReply?(result: HumanResult): void }) {
  const style = message.kind !== ActorKind.User ? { "--harness-color": traceColor(JSON.stringify([message.kind, message.key])) } as CSSProperties : undefined;
  let model: MessageContent | undefined;
  try { model = parseMessageContent(message.content); } catch { /* Invalid persisted model is shown below. */ }
  const actorName = typeof model?.meta.actor_display_name === "string" ? model.meta.actor_display_name : message.kind.split("/").at(-1)!;
  const explicitTitle = model?.meta.title;
  const title = typeof explicitTitle === "string" && explicitTitle ? explicitTitle
    : model?.blocks[0] ? traceLabel(model.blocks[0], index) : `步骤 ${index + 1}`;
  return (
    <article className={`detail-card${style ? " harness-trace" : ""}`} style={style} id={`message-${message.id}`} data-message-id={message.id}>
      <div className="timeline-node">{index + 1}</div>
      <div className="detail-card-head">
        <span className="block-kind" title={`${message.kind} / ${message.key}`}>{actorName.toUpperCase()}</span>
        {message.status !== "completed" && <span className="quiet">{messageStatusLabel(message.status)}</span>}
      </div>
      <div className="detail-card-title">{title}</div>
      <div className="detail-card-time" title={`${message.created_at} → ${message.updated_at}`}>
        {activityTime(message.created_at)} → {activityTime(message.updated_at)}
      </div>
      <ReplyReference message={message} />
      <MessageBody message={message} onReply={onReply} />
    </article>
  );
}

function activityTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString([], {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  });
}

// Operator role messages share the owning Operator's workspace. Run keys remain
// author identities and must not create a separate detail conversation.
export function detailOrganizer(message?: Pick<Message, "kind" | "key" | "target_kind" | "target_key">): DetailSelection["organizer"] {
  if (!message) return undefined;
  // A directed message opens its recipient's workspace; replies to a user and
  // broadcasts from an Operator open the author's workspace instead.
  return operatorActor(message.target_kind, message.target_key) ?? operatorActor(message.kind, message.key);
}

function operatorActor(kind?: ActorKind, key?: string): DetailSelection["organizer"] {
  const owner = kind ? operatorOwner(kind) : undefined;
  if (owner) return { kind: ActorKind.Operator, key: owner };
  return kind === ActorKind.Operator && key ? { kind: ActorKind.Operator, key } : undefined;
}
