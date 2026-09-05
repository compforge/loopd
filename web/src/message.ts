import { applyPatch, parseUIModel } from "@compforge/agentue/ui";
import type { Message, MessageEvent } from "./api";

// +spec=`Message ID owns the snapshot; equal block IDs in parallel questions never collide`
export function mergeMessage(messages: Message[], incoming: Message): Message[] {
  const existing = messages.find((m) => m.id === incoming.id);
  if (existing && (existing.revision ?? 0) > (incoming.revision ?? 0)) return messages;
  return [...messages.filter((m) => m.id !== incoming.id), incoming].sort((a, b) => a.id.localeCompare(b.id));
}
export function applyMessageEvent(messages: Message[], delivery: MessageEvent): Message[] {
  const { messageID, message, event } = delivery;
  if (!messageID || !message || message.id !== messageID) throw new Error("Message event identity mismatch");
  const existing = messages.find((m) => m.id === messageID);
  if (existing && ((existing.revision ?? 0) > event.seq || (event.op !== "start" && (existing.revision ?? 0) === event.seq))) return messages;
  const snapshot = applyPatch(structuredClone(existing?.content ?? {}), event);
  const model = parseUIModel(snapshot);
  if (event.op === "end") model.meta.output = { ended: true };
  return mergeMessage(messages, { ...message, revision: event.seq, content: model });
}
