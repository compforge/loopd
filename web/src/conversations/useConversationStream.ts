import { useEffect, useRef } from "react";
import { streamConversation } from "./api";
import type { MessageEvent } from "../messages/types";

// A subscription outlives individual messages. Each connection attempt owns its
// repair request and abort signal, so reconnect never leaves an old reader alive.
export function useConversationStream(
  conversationID: string | undefined,
  onEvent: (event: MessageEvent) => void,
  onReady?: (signal: AbortSignal) => Promise<unknown>,
  onError?: (cause: unknown) => void,
) {
  const callbacks = useRef({ onEvent, onReady, onError });
  callbacks.current = { onEvent, onReady, onError };
  useEffect(() => {
    if (!conversationID) return;
    // Bind callbacks to this conversation, not whichever view renders next.
    const { onEvent, onReady, onError } = callbacks.current;
    let stopped = false;
    let attempt: AbortController;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function connect() {
      attempt = new AbortController();
      const controller = attempt;
      let connected = false;
      try {
        await streamConversation(conversationID!, controller.signal, (event) => {
          if (controller.signal.aborted) return;
          if (!connected) {
            connected = true;
            void onReady?.(controller.signal).catch((cause: unknown) => {
              if (!controller.signal.aborted) onError?.(cause);
            });
          }
          onEvent(event);
        });
      } catch (cause) {
        if (!controller.signal.aborted) onError?.(cause);
      } finally {
        controller.abort();
        if (!stopped) timer = setTimeout(connect, 1500);
      }
    }
    void connect();
    return () => {
      stopped = true;
      attempt?.abort();
      clearTimeout(timer);
    };
  }, [conversationID]);
}
