import { decodeSse, type SseMessage, type UIModel } from "@compforge/agentue/ui";

export type ActorKind = "user" | "operator" | "harness";
export type TargetActorKind = Exclude<ActorKind, "user">;

export interface Actor {
  kind: TargetActorKind;
  key: string;
  display_name?: string;
  description?: string;
}

export interface Conversation {
  id: string;
  name?: string;
  parent_message_id?: string;
  created_at: string;
  updated_at: string;
}

export interface Message {
  id: string;
  reply_to_message_id?: string;
  purpose?: "input" | "response" | "human_request" | "human_reply";
  revision?: number;
  conversation_id: string;
  task_id: string;
  kind: ActorKind;
  key: string;
  content: UIModel;
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

export async function findDetailConversation(messageID: string, signal?: AbortSignal): Promise<Conversation | undefined> {
	const page = await requestJSON<Page<Conversation>>(
		`/v1/conversations?parent_message_id=${encodeURIComponent(messageID)}`, { signal },
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

export async function listMessages(conversationID: string, signal?: AbortSignal): Promise<Message[]> {
  const messages: Message[] = [];
  let after = "";
  for (;;) {
    const page = await requestJSON<Page<Message>>(
      `/v1/conversations/${encodeURIComponent(conversationID)}/messages?limit=100&after=${encodeURIComponent(after)}`,
      { signal },
    );
    messages.push(...page.data);
    if (page.data.length < 100) return messages;
    after = page.data[page.data.length - 1].id;
  }
}

export interface StreamRequest {
  conversationID: string;
  taskID?: string;
  lastEventID?: string;
  text?: string;
  target?: Pick<Actor, "kind" | "key">;
  signal?: AbortSignal;
  onTaskID(taskID: string): void;
  onEvent(message: MessageEvent): void;
}

export async function streamMessage(request: StreamRequest): Promise<void> {
  const replay = Boolean(request.taskID);
  const body = replay
    ? { task_id: request.taskID }
    : {
        user_key: "web-user",
        target: request.target,
        content: textModel(request.text ?? ""),
      };
  const headers: Record<string, string> = {
    Accept: "text/event-stream",
    "Content-Type": "application/json",
  };
  if (request.lastEventID) headers["Last-Event-ID"] = request.lastEventID;
  const response = await fetch(
    `/v1/conversations/${encodeURIComponent(request.conversationID)}/messages`,
    { method: "POST", headers, body: JSON.stringify(body), signal: request.signal },
  );
  if (!response.ok) throw await responseError(response);
  if (!response.body) throw new Error("loop-server returned an empty event stream");
  const taskID = response.headers.get("X-Loopd-Task-ID");
  if (!taskID) throw new Error("loop-server response omitted task ID");
  request.onTaskID(taskID);

  const decoder = new TextDecoder();
  const frames = new SseFrameDecoder();
  const reader = response.body.getReader();
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) break;
    for (const frame of frames.push(decoder.decode(chunk.value, { stream: true }))) {
      request.onEvent(decodeMessageFrame(frame));
    }
  }
  for (const frame of frames.push(decoder.decode())) request.onEvent(decodeMessageFrame(frame));
  const tail = frames.finish();
  if (tail) request.onEvent(decodeMessageFrame(tail));
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

function textModel(text: string): UIModel {
  return {
    version: "1.0",
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

export type MessageEvent = SseMessage & { messageID?: string; message?: Message };

// The envelope belongs to loopd. AgentUE still validates an unchanged event.
export function decodeMessageFrame(frame: string): MessageEvent {
  const lines = frame.split("\n");
  const raw = lines.filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
  const envelope = JSON.parse(raw) as { message_id?: string; message?: Message; event?: unknown };
  if (!envelope.message_id) return decodeSse(frame);
  const inner = lines.filter((line) => !line.startsWith("data:")).join("\n") + `\ndata: ${JSON.stringify(envelope.event)}`;
  return { ...decodeSse(inner), messageID: envelope.message_id, message: envelope.message };
}

export interface HumanReply {
  reply_to_message_id: string;
  outcome: "success" | "dismissed";
  value?: string;
}
export interface HumanResult { message: Message; reply?: Message; status: string; value?: string }
export async function replyHuman(message: Message, reply: HumanReply): Promise<HumanResult> {
  return requestJSON<HumanResult>(`/v1/conversations/${encodeURIComponent(message.conversation_id)}/tasks/${encodeURIComponent(message.task_id)}/replies`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(reply),
  });
}
