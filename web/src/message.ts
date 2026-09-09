import { parseMessageContent } from "./content";
import { applyPatch } from "@compforge/agentue/ui";
import type { Message, MessageEvent } from "./api";

export function messageStatusLabel(status: Message["status"]): string {
  return { streaming: "生成中", completed: "已发送", failed: "输出失败", cancelled: "已取消", expired: "已过期" }[status];
}

// +spec=`Message ID owns the snapshot; equal block IDs in parallel questions never collide`
export function mergeMessage(messages: Message[], incoming: Message): Message[] {
  const existing = messages.find((m) => m.id === incoming.id);
  if (existing && (existing.revision ?? 0) > (incoming.revision ?? 0)) return messages;
  // Metadata discovery must not erase an already loaded body at the same revision.
  if (existing?.content && !incoming.content && (existing.revision ?? 0) === (incoming.revision ?? 0)) {
    incoming = { ...incoming, content: existing.content };
  }
  return [...messages.filter((m) => m.id !== incoming.id), incoming].sort((a, b) => a.id.localeCompare(b.id));
}
export function applyMessageEvent(messages: Message[], delivery: MessageEvent): Message[] {
  const { message, event } = delivery;
  const messageID = event.stream_id;
  if (!messageID || (message && message.id !== messageID)) throw new Error("Message event identity mismatch");
  const existing = messages.find((m) => m.id === messageID);
  if (existing && ((existing.revision ?? 0) > event.seq || (event.op !== "start" && (existing.revision ?? 0) === event.seq))) return messages;
  const base = message ?? existing;
  if (!base) throw new Error("Message event requires an initial snapshot");
  // Metadata alone is not a patch base. A visible body read or Start snapshot
  // restores it; never apply a delta to a fabricated empty model. Keep its
  // watermark so an in-flight older body cannot restore a base missing events.
  if (!base.content && !existing?.content && event.op !== "start") {
    return mergeMessage(messages, { ...base, revision: event.seq });
  }
  const initial = event.op === "start" ? event.model : existing?.content ?? base.content;
  const snapshot = applyPatch(structuredClone(initial!), event);
  const model = parseMessageContent(snapshot);
  return mergeMessage(messages, { ...base, revision: event.seq, content: model });
}
