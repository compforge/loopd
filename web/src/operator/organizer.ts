import { ActorKind, operatorOwner } from "../actors/model";
import type { Message } from "../messages/types";
import type { DetailSelection } from "./types";

// Operator role messages share the owning Operator's workspace. Run keys remain
// author identities and must not create a separate detail conversation.
export function detailOrganizer(
  message?: Pick<Message, "source_kind" | "source_key" | "target_kind" | "target_key">,
): DetailSelection["organizer"] {
  if (!message) return undefined;
  // A directed message opens its recipient's workspace; replies to a user and
  // broadcasts from an Operator open the author's workspace instead.
  return (
    operatorActor(message.target_kind, message.target_key) ??
    operatorActor(message.source_kind, message.source_key)
  );
}

function operatorActor(kind?: ActorKind, key?: string): DetailSelection["organizer"] {
  const owner = kind ? operatorOwner(kind) : undefined;
  if (owner) return { kind: ActorKind.Operator, key: owner };
  return kind === ActorKind.Operator && key ? { kind: ActorKind.Operator, key } : undefined;
}
