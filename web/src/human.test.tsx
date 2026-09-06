import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { decodeMessageFrame, type Message } from "./api";
import { applyMessageEvent } from "./message";
import { MessageBody } from "./MessageBody";
import type { HumanCard } from "./card";

function message(id: string, type = "ask"): Message {
 return { status: "completed", id, task_id: "task", conversation_id: "conv", kind: "operator", key: "router", purpose: "human_request", revision: 1, reply_to_id: "input", created_at: "", updated_at: "", content: { version: "1.0", biz: "chat", meta: {}, blocks: [{ id: "human", type, title: id, prompt: "Choose", status: "pending", deadline: "2030-01-01T00:00:00Z", choices: [{value: "small", label: "Small"}], allow_other: true }] } };
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
 it("renders a reply without the original message loaded and preserves its selected label", () => {
  const reply = message("reply"); reply.kind = "user"; reply.purpose = "human_reply"; reply.reply_to_id = "question";
  reply.content.blocks = [{ id: "human", type: "human_reply", outcome: "success", value: "small" }];
  reply.card = card("reply", "success", "small");
  const html = renderToStaticMarkup(<MessageBody message={reply} onReply={() => {}} />);
  expect(html).toContain("Small"); expect(html).toContain('checked=""'); expect(html).toContain("is-selected");
  expect(html).toContain('disabled=""'); expect(html).not.toContain("<button"); expect(html).not.toContain("textarea");
  reply.card = { type: "content", editable: false };
  expect(renderToStaticMarkup(<MessageBody message={reply} />)).toContain("small");
 });
 it("keeps options visible for terminal questions and distinguishes cancellation from refusal", () => {
  const m = message("budget", "confirm");
  m.card = card("request", "pending"); m.card.type = "confirm";
  m.card.question.confirm_label = "Deploy"; m.card.question.decline_label = "Skip";
  let html = renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("Deploy"); expect(html).toContain("Skip"); expect(html).toContain("忽略 / 取消");
  for (const [status, label] of [["timeout", "已超时"], ["dismissed", "已忽略"]] as const) {
   m.card.question.status = status; m.card.editable = false;
   html = renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
   expect(html).toContain(label); expect(html).toContain("Deploy"); expect(html).not.toContain("<button");
   expect(html).not.toContain('checked=""'); expect(html).not.toContain('role="alert"');
  }
  m.card.question.status="success"; m.card.selected_value="declined";
  html=renderToStaticMarkup(<MessageBody message={m} />);
  expect(html).toContain('checked=""'); expect(html).toContain('value="declined"'); expect(html).not.toContain("已忽略");
 });
 it("renders free text answers read-only and keeps pending input available", () => {
  const m=message("scope"); m.card=card("request","pending");
  expect(renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />)).toContain("textarea");
  m.card=card("reply","success","custom answer");
  const html=renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("custom answer"); expect(html).not.toContain("textarea"); expect(html).not.toContain('checked=""');
 });
});

function card(mode: "request"|"reply", status: HumanCard["question"]["status"], value?: string): HumanCard {
 return { type:"ask", mode, question_id:"question", editable:mode==="request"&&status==="pending", selected_value:value,
  question:{id:"human",type:"ask",title:"Scope",prompt:"Choose",status,deadline:"2030-01-01T00:00:00Z",choices:[{value:"small",label:"Small"},{value:"large",label:"Large"}],allow_other:true} };
}

it("renders mixed content blocks without interpreting plain text as Markdown or executing HTML", () => {
 const m=message("content");m.purpose="output";m.card={type:"content",editable:false};
 m.content.blocks=[{id:"plain",type:"text",content:"**plain**"},{id:"md",type:"markdown",content:"**bold**\n\n`code`\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))"},{id:"tool",type:"tool",name:"Search",status:"completed",content:"Found result"}];
 const html=renderToStaticMarkup(<MessageBody message={m} />);
 expect(html).toContain("**plain**");expect(html).toContain("<strong>bold</strong>");expect(html).toContain("<code>code</code>");
 expect(html).toContain("Search");expect(html).toContain("Found result");expect(html).not.toContain("<script>");expect(html).not.toContain('href="javascript:');
});
