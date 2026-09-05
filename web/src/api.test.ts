import { afterEach, describe, expect, it, vi } from "vitest";
import { decodeSse } from "@compforge/agentue/ui";
import { findDetailConversation, listConversations, listMessages, SseFrameDecoder } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("Conversation navigation", () => {
  it("separates user navigation from work conversation lookup by Task ID", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(Response.json({ data: [{ id: "root" }] }))
      .mockResolvedValueOnce(Response.json({ data: [{ id: "child", task_id: "task/1" }] }))
      .mockResolvedValueOnce(Response.json({ data: [] }));
    vi.stubGlobal("fetch", fetch);
    expect(await listConversations()).toEqual([{ id: "root" }]);
    expect(await findDetailConversation("task/1")).toEqual({ id: "child", task_id: "task/1" });
    expect(await findDetailConversation("task/2")).toBeUndefined();
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      "/v1/conversations?limit=100",
      "/v1/conversations?task_id=task%2F1",
      "/v1/conversations?task_id=task%2F2",
    ]);
  });
  it("reads all child Messages rather than truncating long tasks to one page", async () => {
    const first = Array.from({ length: 100 }, (_, index) => ({ id: `m-${index}` }));
    const fetch = vi.fn().mockResolvedValueOnce(Response.json({ data: first }))
      .mockResolvedValueOnce(Response.json({ data: [{ id: "m-100" }] }));
    vi.stubGlobal("fetch", fetch);
    expect(await listMessages("child")).toHaveLength(101);
    expect(fetch.mock.calls[1][0]).toBe("/v1/conversations/child/messages?limit=100&after=m-99");
  });
});

describe("SseFrameDecoder", () => {
  it("reassembles chunked CRLF frames and preserves event IDs", () => {
    const decoder = new SseFrameDecoder();
    expect(decoder.push("id: 1-0\r")).toEqual([]);
    const frames = decoder.push('\ndata: {"op":"ping","seq":1}\r\n\r\n');
    expect(frames).toHaveLength(1);
    expect(decodeSse(frames[0])).toEqual({
      eventId: "1-0",
      event: { op: "ping", seq: 1 },
    });
    expect(decoder.finish()).toBeUndefined();
  });
});
