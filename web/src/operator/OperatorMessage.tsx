import { ActorKind } from "../actors/model";
import { MessageBody } from "../messages/MessageBody";
import { ReplyReference } from "../messages/ReplyReference";
import type { CSSProperties } from "react";
import type { Message, HumanResult } from "../messages/types";
import { parseMessageContent, type MessageContent } from "../messages/content";
import { messageStatusLabel } from "../messages/state";
import { traceColor, traceLabel } from "./trace";

export function OperatorMessage({
  message,
  index,
  onReply,
  onLoad,
}: {
  message: Message;
  index: number;
  onReply?(result: HumanResult): void;
  onLoad?(message: Message): void;
}) {
  const style =
    message.source_kind !== ActorKind.User
      ? ({
          "--harness-color": traceColor(JSON.stringify([message.source_kind, message.source_key])),
        } as CSSProperties)
      : undefined;
  let model: MessageContent | undefined;
  try {
    model = parseMessageContent(message.content);
  } catch {
    /* Invalid persisted model is shown below. */
  }
  const actorName =
    typeof model?.meta.actor_display_name === "string"
      ? model.meta.actor_display_name
      : message.source_kind.split("/").at(-1)!;
  const explicitTitle = model?.meta.title;
  const title =
    typeof explicitTitle === "string" && explicitTitle
      ? explicitTitle
      : model?.blocks[0]
        ? traceLabel(model.blocks[0], index)
        : `步骤 ${index + 1}`;
  return (
    <article
      className={`detail-card${style ? " harness-trace" : ""}`}
      style={style}
      id={`message-${message.id}`}
      data-message-id={message.id}
    >
      <div className="timeline-node">{index + 1}</div>
      <div className="detail-card-head">
        <span className="block-kind" title={`${message.source_kind} / ${message.source_key}`}>
          {actorName.toUpperCase()}
        </span>
        {message.status !== "completed" && (
          <span className="quiet">{messageStatusLabel(message.status)}</span>
        )}
      </div>
      <div className="detail-card-title">{title}</div>
      <div className="detail-card-time" title={`${message.created_at} → ${message.updated_at}`}>
        {activityTime(message.created_at)} → {activityTime(message.updated_at)}
      </div>
      <ReplyReference message={message} onLoad={onLoad} />
      <MessageBody message={message} onReply={onReply} />
    </article>
  );
}

function activityTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "—"
    : date.toLocaleString([], {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      });
}
