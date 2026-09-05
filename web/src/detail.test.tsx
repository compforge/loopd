import { expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { DetailMessage, detailOrganizer } from "./DetailPanel";
import { groupParallelMessages } from "./parallel";
import type { Message } from "./api";

it("distinguishes custom roles with a shared Run key and renders persisted report errors", () => {
  const messages = ["manager", "executor", "auditor"].map((role): Message => ({
    id: role, task_id: "task", conversation_id: "work", kind: `operator/longhorizon/${role}`, key: "run-uid", purpose: "output",
    created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:01:00Z",
    content: { version: "1.0", biz: "chat", meta: { title: "Round 2", actor_display_name: role }, blocks: [{ id: "report", type: "text", content: "Observed artifact", error: "Command timed out" }] },
  }));
  expect(groupParallelMessages(messages)[0].columns).toHaveLength(3);
  for (const message of messages) {
    const html = renderToStaticMarkup(<DetailMessage message={message} index={0} />);
    expect(html).toContain(message.id.toUpperCase());
    expect(detailOrganizer(message)).toEqual({ kind: "operator", key: "longhorizon" });
    expect(html).toContain(`${message.kind} / run-uid`);
    expect(html).toContain("Round 2");
    expect(html).toContain("Observed artifact");
    expect(html).toContain("Command timed out");
  }
});
