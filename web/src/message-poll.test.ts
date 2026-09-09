import { afterEach, expect, it, vi } from "vitest";
import { MessagePoller } from "./message-poll";
import type { Message } from "./api";

afterEach(() => vi.unstubAllGlobals());

const row = (n: number) => ({ id: String(n).padStart(4,"0"), status: "completed", revision: 1 });
const page = (from: number, size: number) => Array.from({length: size}, (_, i) => row(from+i));

it("loads only the latest page; older history and new discovery keep independent cursors", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(Response.json({data: page(71,30).reverse()}))
    .mockResolvedValueOnce(Response.json({data: page(41,30).reverse()}))
    .mockResolvedValueOnce(Response.json({data: [row(101)]}))
    .mockResolvedValueOnce(Response.json({data: page(38,3).reverse()}));
  vi.stubGlobal("fetch",fetch);
  const poller = new MessagePoller("conv");
  expect((await poller.poll()).map(m=>m.id)).toEqual(page(71,30).map(m=>m.id));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(poller.hasOlder).toBe(true);
  expect(await poller.older()).toHaveLength(30);
  expect((await poller.poll())[0].id).toBe("0101");
  expect(await poller.older()).toHaveLength(3);
  expect(poller.hasOlder).toBe(false);
  expect(await poller.older()).toEqual([]);
  expect(fetch.mock.calls.map(([url])=>url)).toEqual([
    "/v1/conversations/conv/messages?limit=30&order=desc",
    "/v1/conversations/conv/messages?limit=30&before=0071&order=desc",
    "/v1/conversations/conv/messages?limit=30&after=0100&order=asc",
    "/v1/conversations/conv/messages?limit=30&before=0041&order=desc",
  ]);
});

it("reconnect waits for in-flight initialization then publishes bounded catch-up pages without content reads",async()=>{
  let finish!: (response:Response)=>void;
  const fetch=vi.fn()
    .mockImplementationOnce(()=>new Promise<Response>(resolve=>{finish=resolve;}))
    .mockResolvedValueOnce(Response.json({data:[{...row(1), status:"expired", revision:2}]}))
    .mockResolvedValueOnce(Response.json({data:page(2,30)}))
    .mockResolvedValueOnce(Response.json({data:page(32,30)}))
    .mockResolvedValueOnce(Response.json({data:page(62,5)}));
  vi.stubGlobal("fetch",fetch);
  const poller=new MessagePoller("conv");
  const history=poller.poll();
  expect(poller.poll()).toBe(history);
  const batches:Message[][]=[];
  const sync=poller.sync(new AbortController().signal,rows=>batches.push(rows));
  finish(Response.json({data:[row(1)]}));
  expect(await history).toHaveLength(1);
  await sync;
  expect(batches.map(rows=>rows.length)).toEqual([1,30,30,5]);
  expect(batches[0][0].status).toBe("expired");
  expect(fetch).toHaveBeenCalledTimes(5);
  expect(fetch.mock.calls.map(([url])=>url)).toEqual([
    "/v1/conversations/conv/messages?limit=30&order=desc",
    "/v1/conversations/conv/messages?limit=30&ids=0001",
    "/v1/conversations/conv/messages?limit=30&after=0001&order=asc",
    "/v1/conversations/conv/messages?limit=30&after=0031&order=asc",
    "/v1/conversations/conv/messages?limit=30&after=0061&order=asc",
  ]);
});
it("does not advance a cursor when the selected conversation is cancelled",async()=>{
  const controller=new AbortController();
  const fetch=vi.fn()
    .mockImplementationOnce(()=>{controller.abort();return Promise.resolve(Response.json({data:[row(1)]}));})
    .mockResolvedValueOnce(Response.json({data:[row(2)]}));
  vi.stubGlobal("fetch",fetch);
  const poller=new MessagePoller("conv");
  await expect(poller.poll(controller.signal)).rejects.toThrow();
  expect((await poller.poll())[0].id).toBe("0002");
  expect(fetch.mock.calls[1][0]).toContain("order=desc");
});
