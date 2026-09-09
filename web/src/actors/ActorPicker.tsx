import { ActorKind, actorIdentity, actorLabel, type Actor } from "./model";

export interface ActorPickerProps {
  actors: Actor[];
  selectedActorID?: string;
  onSelect(id: string): void;
}
export function ActorPicker({ actors, selectedActorID, onSelect }: ActorPickerProps) {
  return (
    <label className="actor-picker">
      <span className="actor-dot" />
      <span>发送给</span>
      <select
        aria-label="选择 Actor"
        disabled={actors.length === 0}
        value={selectedActorID ?? ""}
        onChange={(event) => {
          onSelect(event.target.value);
        }}
      >
        {actors.length === 0 && <option value="">暂无可用 Actor</option>}
        {actors.filter((actor) => actor.kind === ActorKind.Operator).length > 0 && (
          <optgroup label="Operators">
            {actors
              .filter((actor) => actor.kind === ActorKind.Operator)
              .map((actor) => (
                <option key={actorIdentity(actor)} value={actorIdentity(actor)}>
                  {actorLabel(actor)}
                </option>
              ))}
          </optgroup>
        )}
        {actors.filter((actor) => actor.kind === ActorKind.Harness).length > 0 && (
          <optgroup label="Harnesses">
            {actors
              .filter((actor) => actor.kind === ActorKind.Harness)
              .map((actor) => (
                <option key={actorIdentity(actor)} value={actorIdentity(actor)}>
                  {actorLabel(actor)}
                </option>
              ))}
          </optgroup>
        )}
        {actors.filter((actor) => actor.kind !== ActorKind.Operator && actor.kind !== ActorKind.Harness)
          .length > 0 && (
          <optgroup label="Actors">
            {actors
              .filter((actor) => actor.kind !== ActorKind.Operator && actor.kind !== ActorKind.Harness)
              .map((actor) => (
                <option key={actorIdentity(actor)} value={actorIdentity(actor)}>
                  {actorLabel(actor)}
                </option>
              ))}
          </optgroup>
        )}
      </select>
    </label>
  );
}
