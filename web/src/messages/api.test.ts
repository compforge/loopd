import { afterEach, describe, expect, it, vi } from "vitest";
import { listMessages, submitMessage } from "./api";

afterEach(() => vi.unstubAllGlobals());

it("submits content with one short JSON request, separately from the conversation stream", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(
    Response.json({
      id: "message",
      conversation_id: "conv",
      task_id: "receipt",
      status: "completed",
      revision: 1,
    }),
  );
  vi.stubGlobal("fetch", fetch);
  const message = await submitMessage({
    conversationID: "conv",
    text: "hello",
    target: { kind: "operator", key: "router" },
  });
  expect(fetch).toHaveBeenCalledTimes(1);
  const [path, request] = fetch.mock.calls[0];
  expect(path).toBe("/v1/conversations/conv/messages");
  expect(JSON.parse(request.body).content.blocks[0].content).toBe("hello");
  expect(message.task_id).toBe("receipt");
  expect(message.content!.blocks[0].content).toBe("hello");
});

describe("Message discovery", () => {
  it("reads one metadata page without fetching bodies or following history", async () => {
    const first = Array.from({ length: 30 }, (_, index) => ({ id: `m-${index}` }));
    const fetch = vi.fn().mockResolvedValue(Response.json({ data: first }));
    vi.stubGlobal("fetch", fetch);
    expect(await listMessages("child", { order: "desc" })).toHaveLength(30);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("/v1/conversations/child/messages?limit=30&order=desc");
  });
});
