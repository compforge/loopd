import { useRef, useState } from "react";
import type { Actor } from "../actors/model";
import type { ActorPickerProps } from "../actors/ActorPicker";
import { submitMessage } from "../messages/api";
import { PendingMessage, Welcome } from "../messages/MessageList";
import { OperatorPanel } from "../operator/OperatorPanel";
import { createConversation } from "./api";
import type { Conversation } from "./types";
import { Composer } from "./Composer";

export function NewConversation({
  actor,
  picker,
  onCreated,
  onSent,
}: {
  actor?: Actor;
  picker: ActorPickerProps;
  onCreated(conversation: Conversation): void;
  onSent(conversation: Conversation): void;
}) {
  const created = useRef<Conversation | undefined>(undefined);
  const [pendingText, setPendingText] = useState<string>();
  async function send(text: string) {
    if (!actor) return;
    setPendingText(text);
    try {
      // Keep a successful creation across a failed submission, so retry does
      // not silently create another conversation. Navigation is owned by App.
      if (!created.current) {
        const name = text.replace(/\s+/g, " ").trim();
        created.current = await createConversation(name.length > 32 ? `${name.slice(0, 32)}…` : name);
        onCreated(created.current);
      }
      await submitMessage({ conversationID: created.current.id, target: actor, text });
      onSent(created.current);
    } finally {
      setPendingText(undefined);
    }
  }
  return (
    <>
      <main className="chat-panel">
        <header className="chat-header">
          <div>
            <span className="eyebrow">CONVERSATION</span>
            <h1>新对话</h1>
          </div>
        </header>
        <section className="messages" aria-live="polite">
          {pendingText ? <PendingMessage text={pendingText} /> : <Welcome description={actor?.description} />}
        </section>
        <Composer actor={actor} picker={picker} onSend={send} />
      </main>
      <OperatorPanel />
    </>
  );
}
