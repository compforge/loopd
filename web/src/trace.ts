import type { BaseBlock } from "@compforge/agentue/ui";

export function traceCallID(block: BaseBlock): string | undefined {
  if (typeof block.call_id === "string" && block.call_id) return block.call_id;
  // Older persisted messages only carry the Adapter's namespaced block ID.
  const [namespace, callID] = block.id.split("/");
  return namespace === "harness" && callID ? callID : undefined;
}

export function traceColor(callID: string): string {
  // Identity, not arrival order, keeps text/tool blocks and replay the same color.
  let hash = 2166136261;
  for (const character of callID) {
    hash = Math.imul(hash ^ character.charCodeAt(0), 16777619);
  }
  return `hsl(${(hash >>> 0) % 360} 45% 34%)`;
}
