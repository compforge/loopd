import type { ActorKind } from "../actors/model";

export interface Conversation {
  id: string;
  name?: string;
  actor_kind: ActorKind;
  actor_key: string;
  parent_id?: string;
  created_at: string;
  updated_at: string;
}
