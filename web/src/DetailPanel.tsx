import { useEffect, useState, type CSSProperties } from "react";
import type { UIModel } from "@compforge/agentue/ui";
import { findDetailConversation, listMessages, type Conversation, type Message } from "./api";
import { detailMessageModel, traceColor, traceLabel } from "./trace";
import { groupParallelMessages } from "./parallel";

interface Detail {
  parentMessageID: string;
  conversation?: Conversation;
  messages: Message[];
  error?: string;
}

/** @spec 详情仅来自选中 Message 的子 Conversation，切换 Message 后不得显示上一个查询的结果。 */
export function DetailPanel({ message, liveModel, running, status }: {
  message?: Message;
  liveModel?: UIModel;
  running: boolean;
  status?: string;
}) {
  const [detail, setDetail] = useState<Detail>();
  const messageID = message?.id;
  useEffect(() => {
    if (!messageID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    // Poll only the selected active detail; deltas still come from the shared
    // Task stream. Completion and reload use the persisted child Messages.
    async function refresh() {
      try {
        const conversation = await findDetailConversation(messageID!, controller.signal);
        const messages = conversation ? await listMessages(conversation.id, controller.signal) : [];
        if (!controller.signal.aborted) setDetail({ parentMessageID: messageID!, conversation, messages });
      } catch (cause) {
        if (!controller.signal.aborted) {
          setDetail({ parentMessageID: messageID!, messages: [], error: String(cause) });
        }
      } finally {
        if (running && !controller.signal.aborted) timer = setTimeout(refresh, 1_000);
      }
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [messageID, running]);

  const selected = detail?.parentMessageID === messageID ? detail : undefined;
  const groups = groupParallelMessages(selected?.messages ?? []);
  const indices = new Map((selected?.messages ?? []).map((item, index) => [item.id, index]));
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
            <div><small>TASK</small><code>{message.task_id.slice(0, 8)}…{message.task_id.slice(-4)}</code></div>
            <span className={`run-badge ${status ?? "completed"}`}>{(status ?? "completed").toUpperCase()}</span>
          </div>
          <div className="timeline">
            {selected.messages.length === 0 && <div className="muted-state">等待处理消息…</div>}
            {groups.map((group) => (
              <section className="detail-group" key={group.columns[0][0].id}>
                <div className="parallel-scroll">
                  <div className="parallel-columns" style={{ gridTemplateColumns: `repeat(${group.columns.length}, 240px)` }}>
                    {group.columns.map((column) => (
                      <div className="parallel-column" key={column[0].id}>
                        {column.map((item) => <DetailMessage key={item.id} message={item} index={indices.get(item.id)!} liveModel={liveModel} />)}
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

function DetailMessage({ message, index, liveModel }: { message: Message; index: number; liveModel?: UIModel }) {
  const style = message.kind === "harness" ? { "--harness-color": traceColor(message.key) } as CSSProperties : undefined;
  let model: UIModel | undefined;
  try { model = detailMessageModel(message, liveModel); } catch { /* Invalid persisted model is shown below. */ }
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
