import { parseUIModel, type BaseBlock, type UIModel } from "@compforge/agentue/ui";
import type { Message } from "./api";

// Only overlay streamed blocks onto a real child Message with matching actor
// identity. Never invent detail Messages from the main response's blocks.
export function detailMessageModel(message: Message, liveModel?: UIModel): UIModel {
  const model = parseUIModel(message.content);
  if (message.kind !== "harness" || !liveModel) return model;
  const blocks = liveModel.blocks.filter((block) => traceCallID(block) === message.key);
  return blocks.length ? { ...model, blocks } : model;
}

export function traceLabel(block: BaseBlock, index: number): string {
  if (typeof block.effect_key === "string" && block.effect_key) return block.effect_key;
  if (block.type === "tool" && typeof block.name === "string" && block.name) return block.name;
  return `步骤 ${index + 1}`;
}

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
