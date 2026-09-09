import { decodeSse } from "@compforge/agentue/ui";
import type { Message, MessageEvent } from "./types";

// Only snapshots have a loopd metadata envelope. AgentUE owns stream addressing.
export function decodeMessageFrame(frame: string): MessageEvent {
  const lines = frame.split("\n");
  const raw = lines
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n");
  const envelope = JSON.parse(raw) as { message?: Message; event?: unknown };
  if (!envelope.event) return decodeSse(frame);
  const inner =
    lines.filter((line) => !line.startsWith("data:")).join("\n") +
    `\ndata: ${JSON.stringify(envelope.event)}`;
  return { ...decodeSse(inner), message: envelope.message };
}
