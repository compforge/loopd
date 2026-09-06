// Actor identity is shared by users, registered services and Operator-owned roles.
export const ActorKind = {
  User: "user",
  Operator: "operator",
  Harness: "harness",
} as const;

export type BuiltinActorKind = typeof ActorKind[keyof typeof ActorKind];
// Keep known values discoverable while allowing actors introduced by consumers.
export type ActorKind = BuiltinActorKind | (string & {});

export interface ActorRef { kind: ActorKind; key: string }
export interface Actor extends ActorRef { display_name?: string; description?: string }

// These helpers interpret the existing Operator namespace for presentation;
// they do not validate or restrict which kinds may be stored or received.
export function operatorOwner(kind: ActorKind): string | undefined {
  return kind.startsWith(`${ActorKind.Operator}/`) ? kind.split("/")[1] || undefined : undefined;
}
export function operatorRole(kind: ActorKind): string | undefined {
  return kind.startsWith(`${ActorKind.Operator}/`) ? kind.split("/").slice(2).join("/") || undefined : undefined;
}
export function isOperatorKind(kind: ActorKind): boolean {
  return kind === ActorKind.Operator || kind.startsWith(`${ActorKind.Operator}/`);
}
