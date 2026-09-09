import { parseUIModel, type BaseBlock, type UIModel } from "@compforge/agentue/ui";

// Message API snapshots contain complete block bodies.
export interface MessageContent extends UIModel {
  blocks: BaseBlock[];
}

export function parseMessageContent(value: unknown): MessageContent {
  const model = parseUIModel(value);
  const blocks = model.blocks.map((block) => {
    if (block.type === undefined) throw new Error(`Message block ${block.id} has not been loaded`);
    return block;
  });
  return { ...model, blocks };
}

export function textModel(text: string): MessageContent {
  return {
    version: "1.1",
    biz: "chat",
    meta: {},
    blocks: [{ id: "question", type: "text", role: "user", content: text }],
  };
}
