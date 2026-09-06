import { useEffect, useRef } from "react";
import { streamConversation, type MessageEvent } from "./api";

// A Conv connection multiplexes Message streams. Reconnect restores active SQL
// snapshots; one message's End never ends this page subscription.
export function useConversationStream(conversationID: string | undefined, onEvent: (event: MessageEvent) => void) {
  const callback = useRef(onEvent);
  callback.current = onEvent;
  useEffect(() => {
    if (!conversationID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function connect() {
      try {
        await streamConversation(conversationID!, controller.signal, (event) => {
          if (!controller.signal.aborted) callback.current(event);
        });
      } catch {
        // Incremental SQL polling repairs bridge/connection outages. Never resend inputs.
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(connect, 1500);
      }
    }
    void connect();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [conversationID]);
}
