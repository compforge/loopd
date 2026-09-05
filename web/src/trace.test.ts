import { describe, expect, it } from "vitest";
import { traceCallID, traceColor, traceLabel } from "./trace";

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
