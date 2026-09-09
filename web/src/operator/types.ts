import type { ActorKind } from "../actors/model";

export interface DetailSelection {
  parentID: string;
  organizer?: { kind: typeof ActorKind.Operator; key: string };
}
