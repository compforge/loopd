import { useCallback, useEffect, useMemo, useState } from "react";
import type { Message, MessageEvent, HumanResult } from "../messages/types";
import { applyMessageEvent, mergeMessage } from "../messages/state";
import { errorMessage } from "../transport/http";
import { MessagePoller } from "./MessagePoller";
import { useConversationStream } from "./useConversationStream";

interface Scope {
  id?: string;
  active: boolean;
  poller?: MessagePoller;
  signal?: AbortSignal;
  loadingOlder: boolean;
}
interface Feed {
  scope: Scope;
  messages: Message[];
  loading: boolean;
  hasOlder: boolean;
  loadingOlder: boolean;
  error?: string;
}
const emptyMessages: Message[] = [];

// One mounted conversation owns its reads, cursors and message state. A callback
// retains that scope even after A -> B -> A; an old A cannot mutate the new A.
export function useConversationMessages(conversationID?: string) {
  const scope = useMemo<Scope>(
    () => ({ id: conversationID, active: false, loadingOlder: false }),
    [conversationID],
  );
  const [feed, setFeed] = useState<Feed>();
  const fail = useCallback(
    (cause: unknown) => {
      if (!scope.active) return;
      setFeed((current) => (current?.scope === scope ? { ...current, error: errorMessage(cause) } : current));
    },
    [scope],
  );
  const merge = useCallback(
    (items: Message[]) => {
      if (!scope.active) return;
      const addressed = items.filter((message) => message.conversation_id === scope.id);
      for (const message of addressed) scope.poller?.observe(message.id);
      setFeed((current) =>
        current?.scope === scope
          ? {
              ...current,
              messages: addressed.reduce(mergeMessage, current.messages),
              hasOlder: scope.poller?.hasOlder ?? false,
            }
          : current,
      );
    },
    [scope],
  );
  const reply = useCallback(
    (result: HumanResult) => {
      merge(result.reply ? [result.message, result.reply] : [result.message]);
    },
    [merge],
  );

  useEffect(() => {
    if (!conversationID) return;
    const controller = new AbortController();
    const poller = new MessagePoller(conversationID);
    scope.active = true;
    scope.poller = poller;
    scope.signal = controller.signal;
    scope.loadingOlder = false;
    setFeed({ scope, messages: [], loading: true, hasOlder: false, loadingOlder: false });
    void poller
      .poll(controller.signal)
      .then((items) => {
        if (!controller.signal.aborted) merge(items);
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) fail(cause);
      })
      .finally(() => {
        if (!controller.signal.aborted)
          setFeed((current) =>
            current?.scope === scope
              ? {
                  ...current,
                  loading: false,
                  hasOlder: poller.hasOlder,
                }
              : current,
          );
      });
    return () => {
      scope.active = false;
      controller.abort();
    };
  }, [conversationID, scope, merge, fail]);

  const receive = useCallback(
    (delivery: MessageEvent) => {
      if (!scope.active || !delivery.event.stream_id) return;
      if (delivery.message && delivery.message.conversation_id !== scope.id) return;
      scope.poller?.observe(delivery.event.stream_id);
      setFeed((current) => {
        if (current?.scope !== scope) return current;
        try {
          return { ...current, messages: applyMessageEvent(current.messages, delivery) };
        } catch (cause) {
          return { ...current, error: errorMessage(cause) };
        }
      });
    },
    [scope],
  );
  const sync = useCallback(
    async (signal: AbortSignal) => {
      const poller = scope.poller;
      if (!scope.active || !poller) return;
      await poller.sync(signal, (items) => {
        if (!signal.aborted) merge(items);
      });
      if (!signal.aborted)
        setFeed((current) => (current?.scope === scope ? { ...current, error: undefined } : current));
    },
    [scope, merge],
  );
  useConversationStream(conversationID, receive, sync, fail);

  const loadOlder = useCallback(
    async (beforeInsert?: () => void) => {
      const { poller, signal } = scope;
      if (!scope.active || !poller || !signal || scope.loadingOlder) return;
      scope.loadingOlder = true;
      setFeed((current) => (current?.scope === scope ? { ...current, loadingOlder: true } : current));
      try {
        const items = await poller.older(signal);
        if (signal.aborted) return;
        beforeInsert?.();
        merge(items);
      } catch (cause) {
        if (!signal.aborted) fail(cause);
      } finally {
        scope.loadingOlder = false;
        if (!signal.aborted)
          setFeed((current) => (current?.scope === scope ? { ...current, loadingOlder: false } : current));
      }
    },
    [scope, merge, fail],
  );

  const current = feed?.scope === scope ? feed : undefined;
  return {
    messages: current?.messages ?? emptyMessages,
    loading: current?.loading ?? !!conversationID,
    hasOlder: current?.hasOlder ?? false,
    loadingOlder: current?.loadingOlder ?? false,
    error: current?.error,
    merge,
    reply,
    loadOlder,
  };
}
