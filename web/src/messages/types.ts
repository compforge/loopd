import type { ActorKind } from "../actors/model";
import type { MessageContent } from "./content";
import type { SseMessage } from "@compforge/agentue/ui";

export interface Message {
  status: "streaming" | "completed" | "failed" | "cancelled" | "expired";
  id: string;
  target_kind?: ActorKind;
  target_key?: string;
  reply_to_id?: string;
  revision?: number;
  conversation_id: string;
  task_id: string;
  source_kind: ActorKind;
  source_key: string;
  content?: MessageContent;
  created_at: string;
  updated_at: string;
}

export type MessageInfo = Omit<Message, "content">;
export type MessageEvent = SseMessage & { message?: Message };

export interface HumanReply {
  reply_to_id: string;
  outcome: "success" | "dismissed";
  value?: string;
}
export interface HumanResult {
  message: Message;
  reply?: Message;
  status: string;
  value?: string;
}
