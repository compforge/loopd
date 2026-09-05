import { applyPatch, parseUIModel } from "@compforge/agentue/ui";
import type { Message, MessageEvent } from "./api";

// +spec=`Message ID owns the snapshot; equal block IDs in parallel questions never collide`
export function mergeMessage(messages: Message[], incoming: Message): Message[] {
  const existing = messages.find((m) => m.id === incoming.id);
  if (existing && (existing.revision ?? 0) > (incoming.revision ?? 0)) return messages;
  return [...messages.filter((m) => m.id !== incoming.id), incoming].sort((a, b) => a.id.localeCompare(b.id));
}
export function applyHumanMessageEvent(messages: Message[], delivery: MessageEvent): Message[] {
  const { messageID, message, event } = delivery;
  if (!messageID || !message || message.id !== messageID) throw new Error("Message event identity mismatch");
  const existing = messages.find((m) => m.id === messageID);
  if (existing && (existing.revision ?? 0) > event.seq) return messages;
  const snapshot = applyPatch(structuredClone(existing?.content ?? {}), event);
  return mergeMessage(messages, { ...message, revision: event.seq, content: parseUIModel(snapshot) });
}

export interface HumanQuestion {
  id: string;
  type: "ask" | "confirm";
  title: string;
  prompt: string;
  status: "pending" | "success" | "dismissed" | "timeout" | "failure";
  deadline: string;
  choices?: { value: string; label: string; description?: string }[];
  allow_other?: boolean;
  confirm_label?: string;
  decline_label?: string;
  reason?: string;
}
export function humanStatus(status: HumanQuestion["status"]): string {
  return { pending: "等待答复", success: "已答复", dismissed: "已忽略", timeout: "已超时", failure: "已结束" }[status];
}
