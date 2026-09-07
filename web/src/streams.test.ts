import { afterEach, expect, it, vi } from "vitest";
import { streamConversation } from "./api";

afterEach(() => vi.unstubAllGlobals());

it("subscribes by Conv without task identity and keeps reading after message End", async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(
    'data: {"stream_id":"a","op":"end","seq":2}\n\n' +
    'data: {"stream_id":"b","op":"set","seq":3,"block":{"id":"text","type":"text","content":"later"}}\n\n',
    { headers: { "Content-Type": "text/event-stream" } },
  ));
  vi.stubGlobal("fetch", fetch);
  const events: string[] = [];
  const controller = new AbortController();
  await streamConversation("conv/one", controller.signal, (delivery) => events.push(delivery.event.stream_id!));
  expect(events).toEqual(["a", "b"]);
  expect(fetch.mock.calls[0]).toEqual([
    "/v1/conversations/conv%2Fone/stream",
    { headers: { Accept: "text/event-stream" }, signal: controller.signal },
  ]);
});
