import type { Conversation } from "./types";

export function ConversationList({
  conversations,
  loading,
  selectedConversationID,
  selectConversation,
  startConversation,
}: {
  conversations: Conversation[];
  loading: boolean;
  selectedConversationID?: string;
  selectConversation(id: string): void;
  startConversation(): void;
}) {
  return (
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
  );
}

function relativeTime(value: string): string {
  const elapsed = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(elapsed) || elapsed < 60_000) return "刚刚";
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  return new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric" }).format(new Date(value));
}
