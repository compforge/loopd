import { describe, expect, it } from "vitest";
import { decodeSse } from "@compforge/agentue/ui";
import { SseFrameDecoder, readSseFrames } from "./sse";

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

it("releases and cancels the response reader when decoding throws", async () => {
  let cancelled = false;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode("data: bad\n\n"));
    },
    cancel() {
      cancelled = true;
    },
  });
  await expect(
    readSseFrames(new Response(body), () => {
      throw new Error("bad frame");
    }),
  ).rejects.toThrow("bad frame");
  expect(cancelled).toBe(true);
  expect(body.locked).toBe(false);
});
