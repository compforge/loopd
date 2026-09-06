import { describe, expect, it } from "vitest";
import { ActorKind, isOperatorKind, operatorOwner, operatorRole, type Actor } from "./actor";
import { detailOrganizer } from "./DetailPanel";
import type { Message } from "./api";

const message: Message = {
  status: "completed", id: "m", conversation_id: "c", task_id: "", key: "run",
  kind: "operator/longhorizon/manager", created_at: "", updated_at: "",
  content: { version: "1.1", biz: "chat", meta: {}, blocks: [] },
};

describe("aggregate actor identity", () => {
  it("classifies built-in participants and custom Operator roles", () => {
    const actors: Actor[] = [ActorKind.User, ActorKind.Operator, ActorKind.Harness, message.kind]
      .map(kind => ({ kind, key: "same" }));
    expect(actors.map(actor => isOperatorKind(actor.kind))).toEqual([false, true, false, true]);
  });

  it("resolves role owners while keeping a Harness with the same key distinct", () => {
    expect(operatorOwner(message.kind)).toBe("longhorizon");
    expect(operatorRole(message.kind)).toBe("manager");
    expect(detailOrganizer(message)).toEqual({ kind: ActorKind.Operator, key: "longhorizon" });
    expect(detailOrganizer({ ...message, kind: ActorKind.Harness, key: "longhorizon" }))
      .toBeUndefined();
    expect(detailOrganizer({ ...message, kind: ActorKind.User, target_kind: message.kind, target_key: "run" }))
      .toEqual({ kind: ActorKind.Operator, key: "longhorizon" });
  });

  it("accepts unknown actor kinds without inventing an Operator workspace", () => {
    for (const kind of ["harness/local", "user/customer", "future-kind"]) {
      const actor: Actor = { kind, key: "run" };
      expect(detailOrganizer({ ...message, ...actor })).toBeUndefined();
      expect(isOperatorKind(kind)).toBe(false);
    }
    expect(isOperatorKind("operator/planner")).toBe(true);
    expect(operatorOwner("operator/longhorizon/manager/delegate")).toBe("longhorizon");
    expect(operatorRole("operator/longhorizon/manager/delegate")).toBe("manager/delegate");
  });
});
