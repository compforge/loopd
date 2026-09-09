import type { ReactNode } from "react";
import { parseMessageContent } from "./content";
import { HumanMessage } from "./blocks/HumanMessage";
import { humanCard } from "./blocks/card";
import { BlockBody } from "./blocks/BlockBody";
import type { HumanResult, Message } from "./types";

export function MessageBody({
  message,
  onReply,
  empty,
}: {
  message: Message;
  onReply?(result: HumanResult): void;
  empty?: ReactNode;
}) {
  if (!message.content) return <div className="message-placeholder quiet">正文按需加载…</div>;
  try {
    const model = parseMessageContent(message.content);
    const card = humanCard(message);
    if (card) return <HumanMessage key={message.id} message={message} card={card} onReply={onReply} />;
    const result = model.blocks.find((block) => block.type === "result");
    const blocks = model.blocks.filter(
      (block) => !(result?.format === "text" && block.type === "text" && block.content === result.content),
    );
    return (
      <div className="message-content">
        {blocks.map((block) => (
          <BlockBody key={block.id} block={block} />
        ))}
        {model.blocks.length === 0 && (empty ?? <span className="quiet">等待输出…</span>)}
        {model.meta.error && <p role="alert">{model.meta.error.message}</p>}
      </div>
    );
  } catch {
    return <p>消息内容无法显示。</p>;
  }
}
