import { afterEach, expect, it, vi } from "vitest";
import { MessageBodyLoader } from "./message-body-loader";

afterEach(() => vi.unstubAllGlobals());

it("bounds visible body reads and skips cancelled queued requests", async () => {
  const release: Array<() => void> = [];
  const fetch = vi.fn((path: string) => new Promise<Response>((resolve) => {
    release.push(() => resolve(Response.json({ id: path.split("/").at(-2) })));
  }));
  vi.stubGlobal("fetch", fetch);
  const loader = new MessageBodyLoader();
  const controller = new AbortController();
  const live = new AbortController().signal;
  const requests = [0,1,2,3].map((id) => loader.load("conv", String(id), live));
  const skipped = loader.load("conv", "cancelled", controller.signal).catch(() => undefined);
  const last = loader.load("conv", "last", live);
  expect(fetch).toHaveBeenCalledTimes(4);
  controller.abort();
  for (const done of release.splice(0)) done();
  await Promise.all(requests);
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(5));
  expect(fetch.mock.calls.some(([path]) => path.includes("cancelled"))).toBe(false);
  release[0]();
  expect((await last).id).toBe("last");
  await skipped;
});
