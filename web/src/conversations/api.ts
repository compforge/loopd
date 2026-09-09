import { requestJSON, responseError, type Page } from "../transport/http";
import { readSseFrames } from "../transport/sse";
import { decodeMessageFrame } from "../messages/events";
import type { MessageEvent } from "../messages/types";
import type { ActorKind } from "../actors/model";
import type { Conversation } from "./types";

export async function listConversations(signal?: AbortSignal): Promise<Conversation[]> {
  const page = await requestJSON<Page<Conversation>>("/v1/conversations?limit=100", { signal });
  return page.data;
}

export async function findDetailConversation(
  parentID: string,
  actorKind: ActorKind,
  actorKey: string,
  signal?: AbortSignal,
): Promise<Conversation | undefined> {
  const page = await requestJSON<Page<Conversation>>(
    `/v1/conversations?parent_id=${encodeURIComponent(parentID)}&actor_kind=${encodeURIComponent(actorKind)}&actor_key=${encodeURIComponent(actorKey)}`,
    { signal },
  );
  return page.data[0];
}

export async function createConversation(name: string, signal?: AbortSignal): Promise<Conversation> {
  return requestJSON<Conversation>("/v1/conversations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
    signal,
  });
}

export async function streamConversation(
  conversationID: string,
  signal: AbortSignal,
  onEvent: (event: MessageEvent) => void,
): Promise<void> {
  const response = await fetch(`/v1/conversations/${encodeURIComponent(conversationID)}/stream`, {
    headers: { Accept: "text/event-stream" },
    signal,
  });
  if (!response.ok) throw await responseError(response);
  await readSseFrames(response, (frame) => onEvent(decodeMessageFrame(frame)));
}
