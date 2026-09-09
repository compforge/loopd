import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { messageBodies } from "./body-loader";
import type { Message } from "./types";

export function ReplyReference({ message, onLoad }: { message: Message; onLoad?(message: Message): void }) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const request = useRef<AbortController | undefined>(undefined);
  const destination = useRef<string | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  useLayoutEffect(() => {
    if (!destination.current) return;
    const element = document.getElementById(destination.current);
    if (element) {
      element.scrollIntoView({ block: "center" });
      destination.current = undefined;
    }
  });
  if (!message.reply_to_id) return null;
  const target = `message-${message.reply_to_id}`;
  return (
    <>
      <a
        className="reply-reference"
        href={`#${target}`}
        onClick={(event) => {
          event.stopPropagation();
          if (document.getElementById(target) || !onLoad) return;
          event.preventDefault();
          if (loading) return;
          const controller = new AbortController();
          request.current = controller;
          setLoading(true);
          setError(undefined);
          // A reference is a point read, not a reason to fetch every intervening page.
          void messageBodies
            .load(message.conversation_id, message.reply_to_id!, controller.signal)
            .then((value) => {
              if (controller.signal.aborted) return;
              destination.current = target;
              onLoad(value);
            })
            .catch((cause: unknown) => {
              if (!controller.signal.aborted) setError(String(cause));
            })
            .finally(() => {
              if (!controller.signal.aborted) setLoading(false);
            });
        }}
      >
        {loading ? "正在查找原消息…" : "查看所回复的消息"}
      </a>
      {error && <small role="alert">{error}</small>}
    </>
  );
}
