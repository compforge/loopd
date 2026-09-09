import { useCallback, useEffect, useState } from "react";
import { listConversations } from "./api";
import type { Conversation } from "./types";
import { errorMessage } from "../transport/http";

export function useConversations() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  useEffect(() => {
    const controller = new AbortController();
    void listConversations(controller.signal)
      .then((items) => {
        if (!controller.signal.aborted)
          setConversations((current) => [
            ...current,
            ...items.filter((item) => !current.some((known) => known.id === item.id)),
          ]);
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(cause));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);
  const add = useCallback((conversation: Conversation) => {
    setConversations((current) => [conversation, ...current.filter((item) => item.id !== conversation.id)]);
  }, []);
  return { conversations, loading, error, add };
}
