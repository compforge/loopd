import { useLayoutEffect, useRef } from "react";
import type { HumanResult } from "../messages/types";
import { LazyMessage } from "../messages/LazyMessage";
import { useConversationMessages } from "../conversations/useConversationMessages";
import { OperatorMessage } from "./OperatorMessage";
import { groupParallelMessages } from "./parallel";
import { useOperatorConversation } from "./useOperatorConversation";
import type { DetailSelection } from "./types";

export function OperatorPanel({
  selection,
  onReply,
}: {
  selection?: DetailSelection;
  onReply?(result: HumanResult): void;
}) {
  const selected = useOperatorConversation(selection);
  const feed = useConversationMessages(selected?.conversation?.id);
  const container = useRef<HTMLDivElement>(null);
  const anchor = useRef<{ id: string; top: number } | undefined>(undefined);
  useLayoutEffect(() => {
    if (!anchor.current || !container.current) return;
    const element = document.getElementById(`message-${anchor.current.id}`);
    if (element) container.current.scrollTop += element.getBoundingClientRect().top - anchor.current.top;
    anchor.current = undefined;
  }, [feed.messages]);
  const loadOlder = () =>
    feed.loadOlder(() => {
      const first = feed.messages[0];
      const element = first && document.getElementById(`message-${first.id}`);
      if (element) anchor.current = { id: first.id, top: element.getBoundingClientRect().top };
    });
  const reply = (result: HumanResult) => {
    feed.reply(result);
    onReply?.(result);
  };
  const actorKey = selection?.organizer?.key;
  const groups = groupParallelMessages(feed.messages);
  const indices = new Map(feed.messages.map((message, index) => [message.id, index]));
  return (
    <aside className="detail-panel">
      <header className="detail-header">
        <span className="eyebrow">CONVERSATION DETAIL</span>
        <h2>处理详情{actorKey ? ` · ${actorKey}` : ""}</h2>
      </header>
      {!selection?.organizer || !selected?.conversation ? (
        <div className="detail-empty">
          <div>◎</div>
          <p>
            {selected?.error ??
              (!selection
                ? "发送消息或选择历史消息，查看相关 Operator 的工作会话。"
                : !selection.organizer
                  ? "这条消息未关联 Operator 工作会话。"
                  : !selected
                    ? `正在查找 ${actorKey} 的工作会话…`
                    : `等待 ${actorKey} 的工作会话…`)}
          </p>
        </div>
      ) : (
        <div className="detail-content" ref={container} data-conversation-id={selected.conversation.id}>
          <div className="task-summary">
            <div>
              <small>OPERATOR CONVERSATION</small>
              <code>{selected.conversation.actor_key}</code>
            </div>
          </div>
          <div className="timeline">
            {feed.hasOlder && (
              <button
                className="load-history"
                type="button"
                disabled={feed.loadingOlder}
                onClick={() => void loadOlder()}
              >
                {feed.loadingOlder ? "加载中…" : "加载更早的消息"}
              </button>
            )}
            {feed.error && <p role="alert">{feed.error}</p>}
            {feed.messages.length === 0 && <div className="muted-state">等待处理消息…</div>}
            {groups.map((group) => (
              <section className="detail-group" key={group.columns[0][0].id}>
                <div className="parallel-scroll">
                  <div
                    className="parallel-columns"
                    style={{ gridTemplateColumns: `repeat(${group.columns.length}, 240px)` }}
                  >
                    {group.columns.map((column) => (
                      <div className="parallel-column" key={column[0].id}>
                        {column.map((message) => (
                          <LazyMessage
                            key={message.id}
                            message={message}
                            onLoad={(value) => feed.merge([value])}
                          >
                            <OperatorMessage
                              message={message}
                              index={indices.get(message.id)!}
                              onLoad={(value) => feed.merge([value])}
                              onReply={reply}
                            />
                          </LazyMessage>
                        ))}
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
