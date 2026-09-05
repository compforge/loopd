import { describe, expect, it } from "vitest";
import type { Message } from "./api";
import { groupParallelMessages } from "./parallel";

const message = (id: string, start: number, end: number): Message => ({
  id, conversation_id: "detail", task_id: "task", kind: "harness", key: id,
  content: { version: "1.0", biz: "chat", meta: {}, blocks: [] },
  created_at: new Date(start).toISOString(), updated_at: new Date(end).toISOString(),
});
const layout = (messages: Message[]) => groupParallelMessages(messages).map((group) => group.columns.map((column) => column.map((item) => item.id)));

describe("parallel detail groups", () => {
  it("keeps serial messages in separate vertical groups", () => {
    expect(layout([message("b", 20, 30), message("a", 0, 10)])).toEqual([[["a"]], [["b"]]]);
  });
  it("puts a containing interval beside shorter serial messages without height metadata", () => {
    const long = message("a", 0, 100);
    expect(layout([long, message("b", 10, 20), message("c", 30, 40)])).toEqual([[["a"], ["b"], ["c"]]]);
    expect(groupParallelMessages([long])[0].columns[0][0]).toBe(long);
  });
  it("gives each actor a column within a connected overlap group", () => {
    expect(layout([message("a", 0, 20), message("b", 10, 40), message("c", 30, 50)])).toEqual([[["a"], ["b"], ["c"]]]);
  });
  it("stacks the same actor and starts independent time groups at the left again", () => {
    const messages = [message("a", 0, 100), message("b", 10, 20), { ...message("c", 30, 40), key: "b" }, message("d", 200, 210)];
    expect(layout(messages)).toEqual([[["a"], ["b", "c"]], [["d"]]]);
  });
  it("includes actor kind in column identity", () => {
    expect(layout([message("a", 0, 20), { ...message("b", 0, 20), key: "a", kind: "operator" }])).toEqual([[["a"], ["b"]]]);
  });
  it("groups equal endpoints and zero-duration messages deterministically after refresh", () => {
    const messages = [message("c", 10, 10), message("b", 10, 20), message("a", 0, 10)];
    expect(layout(messages)).toEqual([[["a"], ["b"], ["c"]]]);
    expect(layout([...messages].reverse())).toEqual(layout(messages));
  });
  it("does not infer overlap from missing dates or stretch reversed intervals", () => {
    const invalid = { ...message("unknown", 0, 0), created_at: "" };
    expect(layout([invalid, message("a", 10, 0), message("b", 11, 12)])).toEqual([[["a"]], [["b"]], [["unknown"]]]);
    expect(layout([])).toEqual([]);
  });
});
