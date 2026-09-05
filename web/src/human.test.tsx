import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { decodeMessageFrame, type Message } from "./api";
import { applyMessageEvent } from "./message";
import { HumanMessage } from "./HumanMessage";

function message(id: string, type = "ask"): Message {
 return { id, task_id: "task", conversation_id: "conv", kind: "operator", key: "router", purpose: "human_request", revision: 1, reply_to_id: "input", created_at: "", updated_at: "", content: { version: "1.0", biz: "chat", meta: {}, blocks: [{ id: "human", type, title: id, prompt: "Choose", status: "pending", deadline: "2030-01-01T00:00:00Z", choices: [{value: "small", label: "Small"}], allow_other: true }] } };
}
describe("Human messages", () => {
 it("routes equal block IDs to different Messages and ignores a stale snapshot", () => {
  const a = message("a"), b = message("b", "confirm");
  b.content.blocks[0].status = "success"; b.revision = 2;
  const frame = `data: ${JSON.stringify({ message_id: "b", message: b, event: { op: "start", seq: 2, model: b.content } })}`;
  const event = decodeMessageFrame(frame);
  const updated = applyMessageEvent([a, message("b", "confirm")], event);
  expect(updated[0].content.blocks[0].status).toBe("pending");
  expect(updated[1].content.blocks[0].status).toBe("success");
  const stale = message("b", "confirm");
  const old = decodeMessageFrame(`data: ${JSON.stringify({ message_id: "b", message: stale, event: { op: "start", seq: 1, model: stale.content } })}`);
  expect(applyMessageEvent(updated, old)).toBe(updated);
 });
 it("only interprets confirmation values through the exact reply reference", () => {
  const reply = message("reply"); reply.kind = "user"; reply.purpose = "human_reply"; reply.reply_to_id = "question";
  reply.content.blocks = [{ id: "human", type: "human_reply", outcome: "success", value: "accepted" }];
  const ask = message("question");
  expect(renderToStaticMarkup(<HumanMessage message={reply} replyTo={ask} onReply={() => {}} />)).toContain("accepted");
  const confirm = message("question", "confirm");
  expect(renderToStaticMarkup(<HumanMessage message={reply} replyTo={confirm} onReply={() => {}} />)).toContain("已同意");
  confirm.id = "unrelated";
  expect(renderToStaticMarkup(<HumanMessage message={reply} replyTo={confirm} onReply={() => {}} />)).toContain("accepted");
 });
 it("renders choices, free text, custom confirm labels and ordinary terminal states", () => {
  const ask = renderToStaticMarkup(<HumanMessage message={message("scope")} onReply={() => {}} />);
  expect(ask).toContain("Small"); expect(ask).toContain("textarea"); expect(ask).toContain("忽略 / 取消");
  const confirm = message("budget", "confirm");
  confirm.content.blocks[0].confirm_label = "Deploy"; confirm.content.blocks[0].decline_label = "Skip";
  const html = renderToStaticMarkup(<HumanMessage message={confirm} onReply={() => {}} />);
  expect(html).toContain("Deploy"); expect(html).toContain("Skip");
  for (const [status, label] of [["timeout", "已超时"], ["dismissed", "已忽略"]]) {
   confirm.content.blocks[0].status = status;
   const ended = renderToStaticMarkup(<HumanMessage message={confirm} onReply={() => {}} />);
   expect(ended).toContain(label); expect(ended).not.toContain("<button"); expect(ended).not.toContain('role="alert"');
  }
 });
});
