import { useEffect, useRef, useState } from "react";
import type { Actor } from "../actors/model";
import type { ActorPickerProps } from "../actors/ActorPicker";
import { submitMessage } from "../messages/api";
import type { Message } from "../messages/types";
import { MessageList } from "../messages/MessageList";
import { OperatorPanel } from "../operator/OperatorPanel";
import { detailOrganizer } from "../operator/organizer";
import type { DetailSelection } from "../operator/types";
import type { Conversation } from "./types";
import { Composer } from "./Composer";
import { useConversationMessages } from "./useConversationMessages";

// App keys this view by navigation identity. Composer and detail selection have
// the same lifetime as the selected conversation; server execution is independent.
export function ConversationChat({
  conversation,
  actor,
  picker,
}: {
  conversation: Conversation;
  actor?: Actor;
  picker: ActorPickerProps;
}) {
  const feed = useConversationMessages(conversation.id);
  const [selectedMessageID, setSelectedMessageID] = useState<string>();
  const [detail, setDetail] = useState<DetailSelection>();
  const [pendingText, setPendingText] = useState<string>();
  const initialized = useRef(false);
  useEffect(() => {
    const latest = feed.messages.at(-1);
    if (initialized.current || !latest) return;
    initialized.current = true;
    setSelectedMessageID(latest.id);
    setDetail({ parentID: conversation.id, organizer: detailOrganizer(latest) });
  }, [conversation.id, feed.messages]);
  const select = (message: Message) => {
    initialized.current = true;
    setSelectedMessageID(message.id);
    setDetail({ parentID: conversation.id, organizer: detailOrganizer(message) });
  };
  async function send(text: string) {
    if (!actor) return;
    initialized.current = true;
    setSelectedMessageID(undefined);
    setDetail({
      parentID: conversation.id,
      organizer: detailOrganizer({
        source_kind: "user",
        source_key: "web-user",
        target_kind: actor.kind,
        target_key: actor.key,
      }),
    });
    setPendingText(text);
    try {
      feed.merge([await submitMessage({ conversationID: conversation.id, target: actor, text })]);
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
            <h1>{conversation.name || "Untitled conversation"}</h1>
          </div>
        </header>
        <MessageList
          {...feed}
          selectedMessageID={selectedMessageID}
          onSelect={select}
          onLoad={(message) => feed.merge([message])}
          onReply={feed.reply}
          pendingText={pendingText}
          description={actor?.description}
        />
        <Composer actor={actor} picker={picker} onSend={send} />
      </main>
      <OperatorPanel selection={detail} onReply={feed.reply} />
    </>
  );
}
