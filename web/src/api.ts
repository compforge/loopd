import type { ActorKind, Actor, ActorRef } from "./actor";
import type { MessageContent } from "./content";
import { decodeSse, type SseMessage } from "@compforge/agentue/ui";

export { ActorKind } from "./actor";
export type { Actor, ActorRef } from "./actor";

export interface Conversation {
  id: string;
  name?: string;
  actor_kind: ActorKind;
  actor_key: string;
  parent_id?: string;
  created_at: string;
  updated_at: string;
}

export interface Message {
  status: "streaming" | "completed" | "failed" | "cancelled" | "expired";
  id: string;
  target_kind?: ActorKind;
  target_key?: string;
  reply_to_id?: string;
  revision?: number;
  conversation_id: string;
  task_id: string;
  kind: ActorKind;
  key: string;
  content: MessageContent;
  created_at: string;
  updated_at: string;
}

export async function listActors(signal?: AbortSignal): Promise<Actor[]> {
  const page = await requestJSON<Page<Actor>>("/v1/actors", { signal });
  return page.data;
}

interface Page<T> {
  data: T[];
}

interface APIErrorEnvelope {
  error?: { message?: string };
}

export async function listConversations(signal?: AbortSignal): Promise<Conversation[]> {
  const page = await requestJSON<Page<Conversation>>("/v1/conversations?limit=100", { signal });
  return page.data;
}

export async function findDetailConversation(parentID: string, actorKind: ActorKind, actorKey: string, signal?: AbortSignal): Promise<Conversation | undefined> {
	const page = await requestJSON<Page<Conversation>>(
		`/v1/conversations?parent_id=${encodeURIComponent(parentID)}&actor_kind=${encodeURIComponent(actorKind)}&actor_key=${encodeURIComponent(actorKey)}`, { signal },
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

type MessageInfo = Omit<Message, "content">;

export async function listMessages(conversationID: string, signal?: AbortSignal, after = ""): Promise<Message[]> {
  const messages: Message[] = [];
  for (;;) {
    const page = await requestJSON<Page<MessageInfo>>(
      `/v1/conversations/${encodeURIComponent(conversationID)}/messages?limit=100&after=${encodeURIComponent(after)}`,
      { signal },
    );
    // Bound body reads independently of metadata discovery.
    for (let i = 0; i < page.data.length; i += 4) {
      const batch = await Promise.all(page.data.slice(i, i + 4).map((m) =>
        requestJSON<Message>(`/v1/conversations/${encodeURIComponent(conversationID)}/messages/${encodeURIComponent(m.id)}/content`,{signal})));
      messages.push(...batch);
    }
    if (page.data.length < 100) return messages;
    after = page.data[page.data.length - 1].id;
  }
}

export async function messageChanges(conversationID: string, revisions: Map<string, number>, signal?: AbortSignal): Promise<Message[]> {
  const entries = [...revisions];
  const messages: Message[] = [];
  for (let i = 0; i < entries.length; i += 100) {
    const watch = entries.slice(i, i + 100).map(([id, revision]) => `${id}:${revision}`).join(",");
    const page = await requestJSON<Page<Message>>(
      `/v1/conversations/${encodeURIComponent(conversationID)}/messages?watch=${encodeURIComponent(watch)}`, { signal },
    );
    messages.push(...page.data);
  }
  return messages;
}

export interface SubmitMessageRequest {
  conversationID: string;
  text?: string;
  target?: ActorRef;
  signal?: AbortSignal;
  onTaskID(taskID: string): void;
  onEvent(message: MessageEvent): void;
}

export async function submitMessage(request: SubmitMessageRequest): Promise<void> {
  const body = {
    user_key: "web-user",
    target: request.target,
    content: textModel(request.text ?? ""),
  };
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  };
  const response = await fetch(
    `/v1/conversations/${encodeURIComponent(request.conversationID)}/messages`,
    { method: "POST", headers, body: JSON.stringify(body), signal: request.signal },
  );
  if (!response.ok) throw await responseError(response);
  const info = (await response.json()) as MessageInfo;
  const message: Message = { ...info, content: body.content };
  request.onTaskID(message.task_id);
  request.onEvent({ message, event: { op: "start", seq: message.revision ?? 1, stream_id: message.id, model: body.content } });

}

export async function streamConversation(conversationID: string, signal: AbortSignal, onEvent: (event: MessageEvent) => void): Promise<void> {
  const response = await fetch(`/v1/conversations/${encodeURIComponent(conversationID)}/stream`, {
    headers: { Accept: "text/event-stream" }, signal,
  });
  if (!response.ok) throw await responseError(response);
  await readMessageStream(response, onEvent);
}

async function readMessageStream(response: Response, onEvent: (event: MessageEvent) => void) {
  if (!response.body) throw new Error("loop-server returned an empty event stream");
  const decoder = new TextDecoder();
  const frames = new SseFrameDecoder();
  const reader = response.body.getReader();
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) break;
    for (const frame of frames.push(decoder.decode(chunk.value, { stream: true }))) {
      onEvent(decodeMessageFrame(frame));
    }
  }
  for (const frame of frames.push(decoder.decode())) onEvent(decodeMessageFrame(frame));
  const tail = frames.finish();
  if (tail) onEvent(decodeMessageFrame(tail));
}

export class SseFrameDecoder {
  private buffer = "";

  push(chunk: string): string[] {
    this.buffer = (this.buffer + chunk).replaceAll("\r\n", "\n");
    const frames: string[] = [];
    for (;;) {
      const boundary = this.buffer.indexOf("\n\n");
      if (boundary < 0) return frames;
      const frame = this.buffer.slice(0, boundary);
      this.buffer = this.buffer.slice(boundary + 2);
      if (frame.trim()) frames.push(frame);
    }
  }

  finish(): string | undefined {
    const frame = this.buffer.trim();
    this.buffer = "";
    return frame || undefined;
  }
}

function textModel(text: string): MessageContent {
  return {
    version: "1.1",
    biz: "chat",
    meta: {},
    blocks: [{ id: "question", type: "text", role: "user", content: text }],
  };
}

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) throw await responseError(response);
  return (await response.json()) as T;
}

async function responseError(response: Response): Promise<Error> {
  try {
    const value = (await response.json()) as APIErrorEnvelope;
    return new Error(value.error?.message || `loop-server returned ${response.status}`);
  } catch {
    return new Error(`loop-server returned ${response.status}`);
  }
}

export type MessageEvent = SseMessage & { message?: Message };

// Only snapshots have a loopd metadata envelope. AgentUE owns stream addressing.
export function decodeMessageFrame(frame: string): MessageEvent {
  const lines = frame.split("\n");
  const raw = lines.filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
  const envelope = JSON.parse(raw) as { message?: Message; event?: unknown };
  if (!envelope.event) return decodeSse(frame);
  const inner = lines.filter((line) => !line.startsWith("data:")).join("\n") + `\ndata: ${JSON.stringify(envelope.event)}`;
  return { ...decodeSse(inner), message: envelope.message };
}

export interface HumanReply {
  reply_to_id: string;
  outcome: "success" | "dismissed";
  value?: string;
}
export interface HumanResult { message: Message; reply?: Message; status: string; value?: string }
export async function replyHuman(message: Message, reply: HumanReply): Promise<HumanResult> {
  return requestJSON<HumanResult>(`/v1/conversations/${encodeURIComponent(message.conversation_id)}/replies`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(reply),
  });
}
