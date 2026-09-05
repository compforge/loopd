import { describe, expect, it } from "vitest";
import { traceCallID, traceColor } from "./trace";

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
