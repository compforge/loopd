import { useEffect, useState, type CSSProperties } from "react";
import { parseUIModel, type UIModel } from "@compforge/agentue/ui";
import { findDetailConversation, listMessages, type Conversation, type Message } from "./api";
import { traceColor, traceLabel } from "./trace";
import { groupParallelMessages } from "./parallel";

interface Detail {
  scope: string;
  conversation?: Conversation;
  messages: Message[];
  error?: string;
}

/** @spec 同一父会话/Operator 的消息共享详情；切换参与者不能泄漏上一个查询的结果。 */
export function DetailPanel({ message, liveMessages, running }: {
  message?: Message;
  liveMessages?: Message[];
  running: boolean;
}) {
  const [detail, setDetail] = useState<Detail>();
  const parentID = message?.conversation_id;
  const actorKind = message?.kind === "user" ? message.target_kind : message?.kind;
  const actorKey = message?.kind === "user" ? message.target_key : message?.key;
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
    else if ((item.revision ?? 0) >= (visible[index].revision ?? 0)) {
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
        <h2>处理详情</h2>
      </header>
      {!message || !selected?.conversation ? (
        <div className="detail-empty">
          <div>◎</div>
          <p>{selected?.error ?? (!message ? "选择一条消息，查看它的处理详情。" : !selected ? "加载中…" : "这条消息暂无详情会话。")}</p>
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
                        {column.map((item) => <DetailMessage key={item.id} message={item} index={indices.get(item.id)!} />)}
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

function DetailMessage({ message, index }: { message: Message; index: number }) {
  const style = message.kind === "harness" ? { "--harness-color": traceColor(message.key) } as CSSProperties : undefined;
  let model: UIModel | undefined;
  try { model = parseUIModel(message.content); } catch { /* Invalid persisted model is shown below. */ }
  const effectKey = model?.meta.effect_key;
  const title = typeof effectKey === "string" && effectKey ? effectKey
    : model?.blocks[0] ? traceLabel(model.blocks[0], index) : `步骤 ${index + 1}`;
  return (
    <article className={`detail-card${style ? " harness-trace" : ""}`} style={style} data-message-id={message.id}>
      <div className="timeline-node">{index + 1}</div>
      <div className="detail-card-head">
        <span className="block-kind">{message.kind.toUpperCase()}</span>
      </div>
      <div className="detail-card-title">{title}</div>
      <div className="detail-card-time" title={`${message.created_at} → ${message.updated_at}`}>
        {activityTime(message.created_at)} → {activityTime(message.updated_at)}
      </div>
      {!model && <p>消息内容无法显示。</p>}
      {model?.blocks.map((block) => (
        <div key={block.id}>
          {block.type === "tool" && <div className="detail-card-subtitle">{String(block.name ?? "TOOL")} {String(block.status ?? "")}</div>}
          {typeof block.content === "string" && <p>{block.content}</p>}
        </div>
      ))}
      {model?.blocks.length === 0 && <p className="quiet">等待输出…</p>}
      {model?.meta.error && <p>{model.meta.error.message}</p>}
    </article>
  );
}

function activityTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString([], {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  });
}
