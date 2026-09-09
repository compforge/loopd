import { requestJSON, type Page } from "../transport/http";
import type { ActorRef } from "../actors/model";
import type { Message, MessageInfo, HumanReply, HumanResult } from "./types";
import { textModel } from "./content";

export const messagePageSize = 30;

// One call discovers one metadata page, never bodies or the rest of history.
export async function listMessages(
  conversationID: string,
  query: { after?: string; before?: string; order?: "asc" | "desc"; ids?: string } = {},
  signal?: AbortSignal,
): Promise<Message[]> {
  const params = new URLSearchParams({ limit: String(messagePageSize), ...query });
  const page = await requestJSON<Page<MessageInfo>>(
    `/v1/conversations/${encodeURIComponent(conversationID)}/messages?${params}`,
    { signal },
  );
  return page.data;
}

export function readMessage(
  conversationID: string,
  messageID: string,
  signal?: AbortSignal,
): Promise<Message> {
  return requestJSON<Message>(
    `/v1/conversations/${encodeURIComponent(conversationID)}/messages/${encodeURIComponent(messageID)}/content`,
    { signal },
  );
}

// A submission returns persisted data; it does not fabricate a stream event.
export async function submitMessage(request: {
  conversationID: string;
  text: string;
  target: ActorRef;
  signal?: AbortSignal;
}): Promise<Message> {
  const content = textModel(request.text);
  const info = await requestJSON<MessageInfo>(
    `/v1/conversations/${encodeURIComponent(request.conversationID)}/messages`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_key: "web-user", target: request.target, content }),
      signal: request.signal,
    },
  );
  return { ...info, content };
}

export async function replyHuman(message: Message, reply: HumanReply): Promise<HumanResult> {
  return requestJSON<HumanResult>(
    `/v1/conversations/${encodeURIComponent(message.conversation_id)}/replies`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(reply),
    },
  );
}
