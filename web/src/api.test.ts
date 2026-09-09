import { afterEach, describe, expect, it, vi } from "vitest";
import { decodeSse } from "@compforge/agentue/ui";
import { findDetailConversation, listConversations, listMessages, submitMessage, SseFrameDecoder } from "./api";

afterEach(() => vi.unstubAllGlobals());

it("submits content with one short JSON request, separately from the conversation stream", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(Response.json({
    id: "message", conversation_id: "conv", task_id: "receipt", status: "completed", revision: 1,
  }));
  vi.stubGlobal("fetch", fetch);
  const onTaskID = vi.fn();
  const onEvent = vi.fn();
  await submitMessage({ conversationID: "conv", text: "hello", target: { kind: "operator", key: "router" }, onTaskID, onEvent });
  expect(fetch).toHaveBeenCalledTimes(1);
  const [path, request] = fetch.mock.calls[0];
  expect(path).toBe("/v1/conversations/conv/messages");
  expect(JSON.parse(request.body).content.blocks[0].content).toBe("hello");
  expect(onTaskID).toHaveBeenCalledWith("receipt");
  expect(onEvent.mock.calls[0][0].message.content.blocks[0].content).toBe("hello");
});

describe("Conversation navigation", () => {
  it("finds a reusable actor conversation by parent and actor", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(Response.json({ data: [{ id: "root" }] }))
      .mockResolvedValueOnce(Response.json({ data: [{ id: "child", parent_id: "root/1" }] }))
      .mockResolvedValueOnce(Response.json({ data: [] }));
    vi.stubGlobal("fetch", fetch);
    expect(await listConversations()).toEqual([{ id: "root" }]);
    expect(await findDetailConversation("root/1", "operator", "router")).toEqual({ id: "child", parent_id: "root/1" });
    expect(await findDetailConversation("root/1", "operator", "other")).toBeUndefined();
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      "/v1/conversations?limit=100",
      "/v1/conversations?parent_id=root%2F1&actor_kind=operator&actor_key=router",
      "/v1/conversations?parent_id=root%2F1&actor_kind=operator&actor_key=other",
    ]);
  });
  it("reads one metadata page without fetching bodies or following history", async () => {
    const first = Array.from({ length: 30 }, (_, index) => ({ id: `m-${index}` }));
    const fetch = vi.fn().mockResolvedValue(Response.json({ data: first }));
    vi.stubGlobal("fetch", fetch);
    expect(await listMessages("child", { order: "desc" })).toHaveLength(30);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("/v1/conversations/child/messages?limit=30&order=desc");
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
