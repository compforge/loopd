import { useEffect, useRef, useState } from "react";
import { useActors } from "../actors/useActors";
import { useConversations } from "../conversations/useConversations";
import { ConversationList } from "../conversations/ConversationList";
import { ConversationChat } from "../conversations/ConversationChat";
import { NewConversation } from "../conversations/NewConversation";

const selectedConversationKey = "loopd.selected-conversation";
export function App() {
  const actors = useActors();
  const history = useConversations();
  const [view, setView] = useState<{ id?: string; epoch: number }>({ epoch: 0 });
  const initialized = useRef(false);
  useEffect(() => {
    if (history.loading || initialized.current) return;
    initialized.current = true;
    const saved = localStorage.getItem(selectedConversationKey);
    const id = history.conversations.find((item) => item.id === saved)?.id ?? history.conversations[0]?.id;
    setView((current) => (current.epoch === 0 ? { ...current, id } : current));
  }, [history.loading, history.conversations]);
  useEffect(() => {
    if (view.id) localStorage.setItem(selectedConversationKey, view.id);
    else if (initialized.current) localStorage.removeItem(selectedConversationKey);
  }, [view.id]);
  const selected = history.conversations.find((conversation) => conversation.id === view.id);
  const picker = { actors: actors.actors, selectedActorID: actors.selectedActorID, onSelect: actors.select };
  return (
    <div className="shell">
      <ConversationList
        conversations={history.conversations}
        loading={history.loading}
        selectedConversationID={view.id}
        selectConversation={(id) =>
          setView((current) => (current.id === id ? current : { id, epoch: current.epoch + 1 }))
        }
        startConversation={() => setView((current) => ({ epoch: current.epoch + 1 }))}
      />
      {selected ? (
        <ConversationChat
          key={view.epoch}
          conversation={selected}
          actor={actors.selectedActor}
          picker={picker}
        />
      ) : (
        <NewConversation
          key={view.epoch}
          actor={actors.selectedActor}
          picker={picker}
          onCreated={history.add}
          onSent={(conversation) => {
            // A request may finish after navigation. Keep its server-side result,
            // but never redirect the user away from their newly selected view.
            setView((current) =>
              current.epoch === view.epoch ? { id: conversation.id, epoch: current.epoch + 1 } : current,
            );
          }}
        />
      )}
      {(history.error || actors.error) && (
        <div className="discovery-error error-banner" role="alert">
          {history.error || actors.error}
        </div>
      )}
    </div>
  );
}
