import { describe, expect, it } from "vitest";
import { detailMessageModel, traceCallID, traceColor, traceLabel } from "./trace";
import type { Message } from "./api";
import type { UIModel } from "@compforge/agentue/ui";

describe("detail Message ownership", () => {
  const content: UIModel = { version: "1.0", biz: "chat", meta: { effect_key: "work/0" }, blocks: [{ id: "a", type: "text", content: "saved" }] };
  const message: Message = { id: "detail-message", conversation_id: "child-conv", task_id: "task", kind: "harness", key: "call-a", content, created_at: "", updated_at: "" };
  const live: UIModel = { ...content, blocks: [
    { id: "a", type: "text", call_id: "call-a", content: "streamed" },
    { id: "b", type: "text", call_id: "call-b", content: "another Harness" },
    { id: "answer", type: "text", content: "main answer" },
  ] };
  it("overlays only the selected child Message's Harness output", () => {
    const result = detailMessageModel(message, live);
    expect(result.blocks).toEqual([live.blocks[0]]);
    expect(result.meta.effect_key).toBe("work/0");
    expect(message.content.blocks[0].content).toBe("saved");
  });
  it("restores persisted content without a stream or without matching output", () => {
    expect(detailMessageModel(message).blocks).toEqual(content.blocks);
    expect(detailMessageModel({ ...message, key: "call-other" }, live).blocks).toEqual(content.blocks);
  });
  it("does not substitute Harness output for another actor", () => {
    expect(detailMessageModel({ ...message, kind: "operator" }, live).blocks).toEqual(content.blocks);
  });
});

describe("Harness trace labels", () => {
  it("shows the effect key instead of execution IDs", () => {
    expect(traceLabel({ id: "harness/call-1/answer", type: "text", call_id: "call-1", effect_key: "route-query" }, 0)).toBe("route-query");
    expect(traceLabel({ id: "harness/call-1/tool", type: "tool", name: "search", effect_key: "work/0" }, 1)).toBe("work/0");
  });

  it("uses readable fallbacks for older messages without effect keys", () => {
    expect(traceLabel({ id: "harness/call-1/answer", type: "text" }, 0)).toBe("步骤 1");
    expect(traceLabel({ id: "harness/call-2/answer", type: "text", call_id: "call-2" }, 1)).toBe("步骤 2");
    expect(traceLabel({ id: "harness/call-1/tool", type: "tool", name: "search" }, 2)).toBe("search");
  });
});

describe("Harness trace identity", () => {
  it("uses the same color for text, tools and replay of a Call", () => {
    const text = { id: "harness/call-1/answer", type: "text" };
    const tool = { id: "harness/call-1/tool/search", type: "tool" };
    const replay = { ...text, call_id: "call-1", effect_key: "work/0" };
    for (const block of [text, tool, replay]) {
      expect(traceCallID(block)).toBe("call-1");
      expect(traceColor(traceCallID(block)!)).toBe(traceColor("call-1"));
    }
    expect(traceColor("call-2")).not.toBe(traceColor("call-1"));
  });

  it("prefers runtime identity and leaves unrelated blocks uncolored", () => {
    expect(traceCallID({ id: "custom-answer", type: "text", call_id: "call-2" })).toBe("call-2");
    expect(traceCallID({ id: "operator/status", type: "text" })).toBeUndefined();
  });
});
