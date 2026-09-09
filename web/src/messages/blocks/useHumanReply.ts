import { useEffect, useRef, useState } from "react";
import { replyHuman } from "../api";
import type { Message, HumanReply, HumanResult } from "../types";
import { errorMessage } from "../../transport/http";

export function useHumanReply(message: Message, questionID: string, onReply?: (result: HumanResult) => void) {
  const active = useRef(false);
  const busy = useRef(false);
  const [state, setState] = useState<{ busy: boolean; value?: string; error?: string }>({ busy: false });
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  async function answer(outcome: HumanReply["outcome"], value?: string) {
    if (busy.current || !active.current || !onReply) return;
    busy.current = true;
    setState({ busy: true, value });
    try {
      const result = await replyHuman(message, { reply_to_id: questionID, outcome, value });
      if (active.current) {
        onReply(result);
        setState({ busy: false });
      }
    } catch (cause) {
      if (active.current) setState({ busy: false, error: errorMessage(cause) });
    } finally {
      busy.current = false;
    }
  }
  return { ...state, answer };
}
