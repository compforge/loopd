import { expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MessageBody } from "./MessageBody";
import type { Message } from "./api";

function message(blocks: unknown[]): Message {
  return { id: "m", kind: "harness", key: "demo", status: "completed", conversation_id: "c", task_id: "", created_at: "", updated_at: "", content: { version: "1.1", biz: "chat", meta: {}, blocks } } as Message;
}
it("renders structured result values including JSON strings without losing their format", () => {
  for (const content of [{ verdict: "passed" }, [1, 2], "quoted", null]) {
    const html = renderToStaticMarkup(<MessageBody message={message([{ id: "result", type: "result", format: "json", content }])} />);
    expect(html).toContain("result-json");
    expect(html).not.toContain("消息内容无法显示");
  }
});
it("shows the final text once while preserving tool and distinct progress blocks", () => {
  const html = renderToStaticMarkup(<MessageBody message={message([
    { id: "progress", type: "text", content: "Working" },
    { id: "answer", type: "text", content: "Finished" },
    { id: "tool", type: "tool", name: "Read", status: "completed" },
    { id: "result", type: "result", format: "text", content: "Finished" },
  ])} />);
  expect(html.match(/Finished/g)).toHaveLength(1);
  expect(html).toContain("Read");
  expect(html).toContain("Working");
});
