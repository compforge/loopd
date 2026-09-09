import { useEffect, useState } from "react";
import { ActorKind, actorIdentity, type Actor } from "./model";
import { listActors } from "./api";
import { errorMessage } from "../transport/http";

const selectedActorKey = "loopd.selected-actor";
export function useActors() {
  const [actors, setActors] = useState<Actor[]>([]);
  const [selectedActorID, setSelectedActorID] = useState<string>();
  const [error, setError] = useState<string>();
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const items = (await listActors(controller.signal)).filter((actor) => actor.kind !== ActorKind.User);
        if (controller.signal.aborted) return;
        setActors(items);
        setError(undefined);
        setSelectedActorID((current) => {
          const saved = current ?? localStorage.getItem(selectedActorKey) ?? undefined;
          const next = items.find((actor) => actorIdentity(actor) === saved) ?? items[0];
          return next ? actorIdentity(next) : undefined;
        });
      } catch (cause) {
        if (!controller.signal.aborted) setError(errorMessage(cause));
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(refresh, 10_000);
      }
    }
    void refresh();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, []);
  const select = (id: string) => {
    setSelectedActorID(id);
    localStorage.setItem(selectedActorKey, id);
  };
  return {
    actors,
    selectedActorID,
    select,
    selectedActor: actors.find((actor) => actorIdentity(actor) === selectedActorID),
    error,
  };
}
