import { describe, expect, it } from "vitest";
import { decodeSse } from "@compforge/agentue/ui";
import { SseFrameDecoder } from "./api";

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
