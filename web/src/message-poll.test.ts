import { afterEach, expect, it, vi } from "vitest";
import { MessagePoller } from "./message-poll";

afterEach(() => vi.unstubAllGlobals());

it("reconciles after an in-flight history read instead of sharing a stale snapshot", async () => {
  let finish!: (response: Response) => void;
  const fetch = vi.fn()
    .mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve; }))
    .mockResolvedValueOnce(Response.json({ id: "a", status: "streaming", revision: 1 }))
    .mockResolvedValueOnce(Response.json({ data: [{ id: "b", status: "completed", revision: 1 }] }))
    .mockResolvedValueOnce(Response.json({ id: "b", status: "completed", revision: 1 }))
    .mockResolvedValueOnce(Response.json({ data: [{ id: "a", status: "expired", revision: 2 }] }));
  vi.stubGlobal("fetch", fetch);
  const poller = new MessagePoller("conv");
  const history = poller.poll();
  const repair = poller.sync();
  finish(Response.json({ data: [{ id: "a", status: "streaming", revision: 1 }] }));
  expect(await history).toHaveLength(1);
  expect((await repair).map((message) => [message.id, message.status])).toEqual([["b", "completed"], ["a", "expired"]]);
  expect(fetch).toHaveBeenCalledTimes(5);
});

it("discovers by ID and stops watching terminal messages", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(Response.json({ data: [{ id: "a", status: "completed", revision: 1 }, { id: "b", status: "streaming", revision: 1 }] }))
    .mockResolvedValueOnce(Response.json({ id: "a", status: "completed", revision: 1 }))
    .mockResolvedValueOnce(Response.json({ id: "b", status: "streaming", revision: 1 }))
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
    "/v1/conversations/conv/messages/a/content",
    "/v1/conversations/conv/messages/b/content",
    "/v1/conversations/conv/messages?limit=100&after=b",
    "/v1/conversations/conv/messages?watch=b%3A1",
    "/v1/conversations/conv/messages?limit=100&after=b",
  ]);
});
