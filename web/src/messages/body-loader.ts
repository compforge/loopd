import { readMessage } from "./api";
import { type Message } from "./types";

interface BodyRequest {
  conversationID: string;
  messageID: string;
  signal: AbortSignal;
  resolve(message: Message): void;
  reject(error: unknown): void;
}

// Only viewport requests enter this queue. A concurrency cap alone must not be
// used as an excuse to enqueue the entire conversation's historical content.
export class MessageBodyLoader {
  private active = 0;
  private queue: BodyRequest[] = [];

  load(conversationID: string, messageID: string, signal: AbortSignal): Promise<Message> {
    return new Promise((resolve, reject) => {
      this.queue.push({ conversationID, messageID, signal, resolve, reject });
      this.drain();
    });
  }

  private drain() {
    while (this.active < 4 && this.queue.length) {
      const request = this.queue.shift()!;
      if (request.signal.aborted) {
        request.reject(request.signal.reason);
        continue;
      }
      this.active++;
      void readMessage(request.conversationID, request.messageID, request.signal)
        .then(request.resolve, request.reject)
        .finally(() => {
          this.active--;
          this.drain();
        });
    }
  }
}

export const messageBodies = new MessageBodyLoader();
