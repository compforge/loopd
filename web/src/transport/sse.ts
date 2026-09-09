// The consumer owns each response reader, including failures in decoding or delivery.
export async function readSseFrames(response: Response, onFrame: (frame: string) => void): Promise<void> {
  if (!response.body) throw new Error("loop-server returned an empty event stream");
  const decoder = new TextDecoder();
  const frames = new SseFrameDecoder();
  const reader = response.body.getReader();
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      for (const frame of frames.push(decoder.decode(chunk.value, { stream: true }))) onFrame(frame);
    }
    for (const frame of frames.push(decoder.decode())) onFrame(frame);
    const tail = frames.finish();
    if (tail) onFrame(tail);
  } finally {
    // Cancellation can itself fail after a network error; preserve the original failure.
    try {
      await reader.cancel();
    } catch {
      /* Reader is already errored. */
    }
    reader.releaseLock();
  }
}

export class SseFrameDecoder {
  private buffer = "";

  push(chunk: string): string[] {
    this.buffer = (this.buffer + chunk).replaceAll("\r\n", "\n");
    const frames: string[] = [];
    for (;;) {
      const boundary = this.buffer.indexOf("\n\n");
      if (boundary < 0) return frames;
      const frame = this.buffer.slice(0, boundary);
      this.buffer = this.buffer.slice(boundary + 2);
      if (frame.trim()) frames.push(frame);
    }
  }

  finish(): string | undefined {
    const frame = this.buffer.trim();
    this.buffer = "";
    return frame || undefined;
  }
}
