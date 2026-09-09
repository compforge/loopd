import { useRef, useState, type FormEvent } from "react";
import { ActorPicker, type ActorPickerProps } from "../actors/ActorPicker";
import { actorName, type Actor } from "../actors/model";
import { errorMessage } from "../transport/http";

export function Composer({
  actor,
  picker,
  onSend,
}: {
  actor?: Actor;
  picker: ActorPickerProps;
  onSend(text: string): Promise<void>;
}) {
  const [draft, setDraft] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const busy = useRef(false);
  const [error, setError] = useState<string>();
  async function send(event: FormEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text || !actor || busy.current) return;
    busy.current = true;
    setSubmitting(true);
    setDraft("");
    setError(undefined);
    try {
      await onSend(text);
    } catch (cause) {
      setError(errorMessage(cause));
      setDraft((current) => current || text);
    } finally {
      busy.current = false;
      setSubmitting(false);
    }
  }
  return (
    <form className="composer" onSubmit={(event) => void send(event)}>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      <textarea
        aria-label="Message"
        placeholder={actor ? `给 ${actorName(actor)} 发一个问题…` : "当前没有可用的 Actor"}
        rows={1}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
            event.preventDefault();
            event.currentTarget.form?.requestSubmit();
          }
        }}
      />
      <button
        className="send-button"
        disabled={!draft.trim() || !actor || submitting}
        type="submit"
        aria-label="Send"
      >
        ↑
      </button>
      <div className="composer-meta">
        <ActorPicker {...picker} />
        <span>Enter 发送 · Shift + Enter 换行</span>
      </div>
    </form>
  );
}
