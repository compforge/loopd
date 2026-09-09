import { useEffect, useRef, useState, type ReactNode } from "react";
import type { Message } from "./types";
import { messageBodies } from "./body-loader";

// Metadata lays out the card; bodies are fetched only near the visible viewport.
// Live SSE snapshots already have content and never need a second body request.
export function LazyMessage({
  message,
  onLoad,
  children,
}: {
  message: Message;
  onLoad(message: Message): void;
  children: ReactNode;
}) {
  const element = useRef<HTMLDivElement>(null);
  const callback = useRef(onLoad);
  callback.current = onLoad;
  const [visible, setVisible] = useState(false);
  const [error, setError] = useState<string>();
  const [attempt, setAttempt] = useState(0);
  const loaded = message.content !== undefined;

  useEffect(() => {
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), {
      rootMargin: "160px",
    });
    if (element.current) observer.observe(element.current);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!visible || loaded) return;
    const controller = new AbortController();
    setError(undefined);
    void messageBodies
      .load(message.conversation_id, message.id, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) callback.current(value);
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) setError(String(cause));
      });
    return () => controller.abort();
  }, [visible, loaded, message.id, message.conversation_id, message.revision, attempt]);

  return (
    <div ref={element} className={loaded ? "lazy-message" : "lazy-message unloaded"}>
      {children}
      {!loaded && error && (
        <button
          className="load-history"
          type="button"
          title={error}
          onClick={() => setAttempt((value) => value + 1)}
        >
          正文加载失败，点击重试
        </button>
      )}
    </div>
  );
}
