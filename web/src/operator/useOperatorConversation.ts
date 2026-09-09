import { useEffect, useState } from "react";
import { findDetailConversation } from "../conversations/api";
import type { Conversation } from "../conversations/types";
import { errorMessage } from "../transport/http";
import type { DetailSelection } from "./types";

// Discovery owns only the parent/organizer association. Message delivery uses
// the same conversation hook as the main chat once the association exists.
export function useOperatorConversation(selection?: DetailSelection) {
  const parentID = selection?.parentID;
  const kind = selection?.organizer?.kind;
  const key = selection?.organizer?.key;
  const scope = JSON.stringify([parentID, kind, key]);
  const [state, setState] = useState<{ scope: string; conversation?: Conversation; error?: string }>();
  useEffect(() => {
    if (!parentID || !kind || !key) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function find() {
      try {
        const conversation = await findDetailConversation(parentID!, kind!, key!, controller.signal);
        if (controller.signal.aborted) return;
        setState({ scope, conversation });
        if (conversation) return;
      } catch (cause) {
        if (!controller.signal.aborted) setState({ scope, error: errorMessage(cause) });
      }
      if (!controller.signal.aborted) timer = setTimeout(find, 2000);
    }
    void find();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [scope, parentID, kind, key]);
  return state?.scope === scope ? state : undefined;
}
