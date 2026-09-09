import { describe, expect, it } from "vitest";
import { parseMessageContent } from "./content";

describe("message content boundary", () => {
  it.each(["1.0", "1.1"])("renders materialized %s messages", (version) => {
    const model = parseMessageContent({
      version,
      biz: "chat",
      meta: {},
      blocks: [{ id: "b2", type: "text", content: "complete" }],
    });
    expect(model.blocks[0].content).toBe("complete");
  });
  it("rejects a storage reference instead of treating it as empty text", () => {
    expect(() =>
      parseMessageContent({ version: "1.1", biz: "chat", meta: {}, blocks: [{ id: "b2", ref: "opaque" }] }),
    ).toThrow("has not been loaded");
  });
});
