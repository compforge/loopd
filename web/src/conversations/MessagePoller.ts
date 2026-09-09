import { listMessages, messagePageSize } from "../messages/api";
import { type Message } from "../messages/types";

// History and discovery have independent cursors. SSE never advances either:
// a later stream event must not hide earlier messages completed while offline.
export class MessagePoller {
  private initialized = false;
  private after = "";
  private before = "";
  private pending?: Promise<Message[]>;
  private olderPending?: Promise<Message[]>;
  hasOlder = false;
  private moreNew = false;
  private known = new Set<string>();

  constructor(private readonly conversationID: string) {}

  observe(id: string) {
    this.known.add(id);
  }

  poll(signal?: AbortSignal): Promise<Message[]> {
    if (!this.pending)
      this.pending = this.read(signal).finally(() => {
        this.pending = undefined;
      });
    return this.pending;
  }

  // Publish each bounded catch-up page immediately, not after the entire backlog.
  async sync(signal: AbortSignal, onPage: (messages: Message[]) => void): Promise<void> {
    if (this.pending) await this.pending;
    // Messages already seen can finish while disconnected (including Human
    // cards whose message status was already completed). Refresh metadata only;
    // a revision change invalidates loaded content until it is visible again.
    const ids = [...this.known];
    for (let i = 0; i < ids.length; i += messagePageSize) {
      signal.throwIfAborted();
      const rows = await listMessages(
        this.conversationID,
        { ids: ids.slice(i, i + messagePageSize).join(",") },
        signal,
      );
      signal.throwIfAborted();
      onPage(rows);
    }
    do {
      signal.throwIfAborted();
      onPage(await this.poll(signal));
    } while (this.moreNew);
  }

  older(signal?: AbortSignal): Promise<Message[]> {
    if (!this.initialized || !this.hasOlder) return Promise.resolve([]);
    if (!this.olderPending)
      this.olderPending = this.readOlder(signal).finally(() => {
        this.olderPending = undefined;
      });
    return this.olderPending;
  }

  private async read(signal?: AbortSignal): Promise<Message[]> {
    const initial = !this.initialized;
    const rows = await listMessages(
      this.conversationID,
      initial ? { order: "desc" } : { after: this.after, order: "asc" },
      signal,
    );
    signal?.throwIfAborted();
    const messages = initial ? rows.toReversed() : rows;
    for (const row of rows) this.observe(row.id);
    if (initial) {
      this.initialized = true;
      this.before = messages[0]?.id ?? "";
      this.hasOlder = rows.length === messagePageSize;
    }
    if (messages.length) this.after = messages.at(-1)!.id;
    this.moreNew = !initial && rows.length === messagePageSize;
    return messages;
  }

  private async readOlder(signal?: AbortSignal): Promise<Message[]> {
    const rows = await listMessages(this.conversationID, { before: this.before, order: "desc" }, signal);
    signal?.throwIfAborted();
    for (const row of rows) this.observe(row.id);
    if (rows.length) this.before = rows.at(-1)!.id;
    this.hasOlder = rows.length === messagePageSize;
    return rows.toReversed();
  }
}
