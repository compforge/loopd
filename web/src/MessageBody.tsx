import Markdown from "react-markdown";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { messageBodies } from "./message-body-loader";
import { parseMessageContent } from "./content";
import { HumanMessage } from "./HumanMessage";
import { humanCard } from "./card";
import { humanStatus, type HumanQuestion } from "./human";
import type { HumanResult, Message } from "./api";

export function MessageBody({ message, onReply, empty }: {
  message: Message; onReply?(result: HumanResult): void; empty?: ReactNode;
}) {
  if (!message.content) return <div className="message-placeholder quiet">正文按需加载…</div>;
  try {
    const card = humanCard(message);
    if (card) return <HumanMessage key={message.id} message={message} card={card} onReply={onReply} />;
    const model = parseMessageContent(message.content);
    const result = model.blocks.find((block) => block.type === "result");
    const blocks = model.blocks.filter((block) => !(result?.format === "text" && block.type === "text" && block.content === result.content));
    return <div className="message-content">
      {blocks.map((block) => <div key={block.id}>
        {block.type === "tool" && <div className="detail-card-subtitle">{String(block.name ?? "TOOL")} {String(block.status ?? "")}</div>}
        {block.type === "result" && block.format === "json" && <pre className="result-json">{JSON.stringify(block.content, null, 2)}</pre>}
        {typeof block.content === "string" && !(block.type === "result" && block.format === "json") && (block.type === "markdown"
          ? <div className="markdown-content"><Markdown>{block.content}</Markdown></div>
          : <p>{block.content}</p>)}
        {block.type === "human_reply" && <p>{block.outcome === "dismissed" ? "已忽略" : String(block.value ?? "已答复")}</p>}
        {(block.type === "ask" || block.type === "confirm") && <><strong>{String(block.title ?? "")}</strong><p>{String(block.prompt ?? "")}</p><small>{humanStatus(block.status as HumanQuestion["status"])}</small></>}
        {typeof block.error === "string" && block.error && <p role="alert">{block.error}</p>}
      </div>)}
      {model.blocks.length === 0 && (empty ?? <span className="quiet">等待输出…</span>)}
      {model.meta.error && <p role="alert">{model.meta.error.message}</p>}
    </div>;
  } catch { return <p>消息内容无法显示。</p>; }
}

export function ReplyReference({ message, onLoad }: { message: Message; onLoad?(message: Message): void }) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const request = useRef<AbortController | undefined>(undefined);
  const destination = useRef<string | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  useLayoutEffect(() => {
    if (!destination.current) return;
    const element = document.getElementById(destination.current);
    if (element) {
      element.scrollIntoView({ block: "center" });
      destination.current = undefined;
    }
  });
  if (!message.reply_to_id) return null;
  const target = `message-${message.reply_to_id}`;
  return <><a className="reply-reference" href={`#${target}`} onClick={(event) => {
    event.stopPropagation();
    if (document.getElementById(target) || !onLoad) return;
    event.preventDefault();
    if (loading) return;
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError(undefined);
    // A reference is a point read, not a reason to fetch every intervening page.
    void messageBodies.load(message.conversation_id, message.reply_to_id!, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return;
        destination.current = target;
        onLoad(value);
      })
      .catch((cause: unknown) => { if (!controller.signal.aborted) setError(String(cause)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
  }}>
    {loading ? "正在查找原消息…" : "查看所回复的消息"}
  </a>{error && <small role="alert">{error}</small>}</>;
}
