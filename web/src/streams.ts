import { useEffect, useRef } from "react";
import { streamConversation, type MessageEvent } from "./api";

// A Conv connection multiplexes Message streams. Reconnect restores active SQL
// snapshots; one message's End never ends this page subscription.
export function useConversationStream(conversationID: string | undefined, onEvent: (event: MessageEvent) => void, onReady?: (signal: AbortSignal) => Promise<unknown>) {
  const callback = useRef(onEvent);
  callback.current = onEvent;
  const ready = useRef(onReady);
  ready.current = onReady;
  useEffect(() => {
    if (!conversationID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function connect() {
      let connected = false;
      try {
        await streamConversation(conversationID!, controller.signal, (event) => {
          if (controller.signal.aborted) return;
          if (!connected) {
            connected = true;
            // Repair the initial-load gap and messages completed while disconnected.
            void ready.current?.(controller.signal);
          }
          callback.current(event);
        });
      } catch {
        // Reconnect reconciles SQL once; live discovery belongs to the server listener.
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(connect, 1500);
      }
    }
    void connect();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [conversationID]);
}
