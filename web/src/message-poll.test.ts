import { afterEach, expect, it, vi } from "vitest";
import { MessagePoller } from "./message-poll";

afterEach(() => vi.unstubAllGlobals());

it("discovers by ID and stops watching terminal messages", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(Response.json({ data: [{ id: "a", status: "completed", revision: 1 }, { id: "b", status: "streaming", revision: 1 }] }))
    .mockResolvedValueOnce(Response.json({ data: [] }))
    .mockResolvedValueOnce(Response.json({ data: [{ id: "b", status: "expired", revision: 2 }] }))
    .mockResolvedValueOnce(Response.json({ data: [] }));
  vi.stubGlobal("fetch", fetch);
  const poller = new MessagePoller("conv");
  expect(await poller.poll()).toHaveLength(2);
  expect((await poller.poll())[0].status).toBe("expired");
  expect(await poller.poll()).toEqual([]);
  expect(fetch.mock.calls.map(([url]) => url)).toEqual([
    "/v1/conversations/conv/messages?limit=100&after=",
    "/v1/conversations/conv/messages?limit=100&after=b",
    "/v1/conversations/conv/messages?watch=b%3A1",
    "/v1/conversations/conv/messages?limit=100&after=b",
  ]);
});
