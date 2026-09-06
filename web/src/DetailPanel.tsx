import { MessageBody, ReplyReference } from "./MessageBody";
import type { HumanResult } from "./api";
import { messageStatusLabel } from "./message";
import { useEffect, useState, type CSSProperties } from "react";
import { parseMessageContent, type MessageContent } from "./content";
import { findDetailConversation, listMessages, type ActorKind, type Conversation, type Message } from "./api";
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
  organizer?: { kind: "operator"; key: string };
}

/** @spec 按父会话/Operator 观察工作会话，不等待主回答；切换参与者不能泄漏上一个查询的结果。 */
export function DetailPanel({ selection, liveMessages, running, onReply }: {
  selection?: DetailSelection;
  liveMessages?: Message[];
  running: boolean;
  onReply?(result: HumanResult): void;
}) {
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
    // The actor's workspace stays observable beyond any single UI delivery.
    async function refresh() {
      try {
        const conversation = await findDetailConversation(parentID!, actorKind!, actorKey!, controller.signal);
        const messages = conversation ? await listMessages(conversation.id, controller.signal) : [];
        if (!controller.signal.aborted) setDetail({ scope, conversation, messages });
      } catch (cause) {
        if (!controller.signal.aborted) {
          setDetail({ scope, messages: [], error: String(cause) });
        }
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(refresh, running ? 1_000 : 2_000);
      }
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [scope, parentID, actorKind, actorKey, running]);

  const selected = detail?.scope === scope ? detail : undefined;
  const visible = [...(selected?.messages ?? [])];
  for (const item of liveMessages ?? []) {
    if (item.conversation_id !== selected?.conversation?.id) continue;
    const index = visible.findIndex((value) => value.id === item.id);
    if (index < 0) visible.push(item);
    else if ((item.revision ?? 0) > (visible[index].revision ?? 0)) {
      // The stream carries content updates; polling refreshes the activity interval.
      visible[index] = { ...item, created_at: visible[index].created_at, updated_at: visible[index].updated_at };
    }
  }
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
                        {column.map((item) => <DetailMessage key={item.id} message={item} index={indices.get(item.id)!} onReply={onReply} />)}
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
  const style = message.kind !== "user" ? { "--harness-color": traceColor(JSON.stringify([message.kind, message.key])) } as CSSProperties : undefined;
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
  if (kind?.startsWith("operator/")) return { kind: "operator", key: kind.split("/")[1] };
  return kind === "operator" && key ? { kind, key } : undefined;
}
