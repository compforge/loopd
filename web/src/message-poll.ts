import { listMessages, messageChanges, type Message } from "./api";
import { parseMessageContent } from "./content";

// One poller belongs to one selected conversation. Only discovery advances after;
// receiving an SSE frame must not skip earlier, not-yet-discovered messages.
export class MessagePoller {
  private after = "";
  private active = new Map<string, number>();
  private pending?: Promise<Message[]>;

  constructor(private readonly conversationID: string) {}

  poll(signal?: AbortSignal): Promise<Message[]> {
    // Initial load, reconnect and submit callbacks may coincide. Share the request
    // instead of racing cursor updates or letting a slow network build a queue.
    if (!this.pending) this.pending = this.read(signal).finally(() => { this.pending = undefined; });
    return this.pending;
  }

  async sync(signal?: AbortSignal): Promise<Message[]> {
    // Do not reuse a history request started before the stream's watermark.
    if (this.pending) await this.pending;
    signal?.throwIfAborted();
    return this.poll(signal);
  }

  private async read(signal?: AbortSignal): Promise<Message[]> {
    const added = await listMessages(this.conversationID, signal, this.after);
    const changed = await messageChanges(this.conversationID, this.active, signal);
    signal?.throwIfAborted();
    if (added.length) this.after = added[added.length - 1].id;
    const updates = [...added, ...changed];
    for (const message of updates) {
      if (isActive(message)) this.active.set(message.id, message.revision ?? 0);
      else this.active.delete(message.id);
    }
    return updates;
  }
}

function isActive(message: Message): boolean {
  if (message.status === "streaming") return true;
  return parseMessageContent(message.content).blocks.some((block) =>
    (block.type === "ask" || block.type === "confirm") && block.status === "pending");
}
