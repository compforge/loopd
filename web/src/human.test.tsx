import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { decodeMessageFrame, type Message } from "./api";
import { applyMessageEvent } from "./message";
import { MessageBody, ReplyReference } from "./MessageBody";
import type { HumanQuestion } from "./human";

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
  reply.content.blocks = [{ id: "human", type: "human_reply", outcome: "success", value: "small", question: question("success", "small") }];
  const html = renderToStaticMarkup(<MessageBody message={reply} onReply={() => {}} />);
  expect(html).toContain("Small"); expect(html).toContain('checked=""'); expect(html).toContain("is-selected");
  expect(html).toContain('disabled=""'); expect(html).not.toContain("<button"); expect(html).not.toContain("textarea");
  expect(html).toContain("Scope"); expect(html).toContain("Choose");
  expect(renderToStaticMarkup(<ReplyReference message={reply} />)).toContain('href="#message-question"');
 });
 it("keeps options visible for terminal questions and distinguishes cancellation from refusal", () => {
  const m = message("budget", "confirm");
  const block = m.content.blocks[0];
  block.confirm_label = "Deploy"; block.decline_label = "Skip";
  let html = renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("Deploy"); expect(html).toContain("Skip"); expect(html).toContain("忽略 / 取消");
  for (const [status, label] of [["timeout", "已超时"], ["dismissed", "已忽略"]] as const) {
   block.status = status;
   html = renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
   expect(html).toContain(label); expect(html).toContain("Deploy"); expect(html).not.toContain("<button");
   expect(html).not.toContain('checked=""'); expect(html).not.toContain('role="alert"');
  }
  block.status="success"; block.selected_value="declined";
  html=renderToStaticMarkup(<MessageBody message={m} />);
  expect(html).toContain('checked=""'); expect(html).toContain('value="declined"'); expect(html).not.toContain("已忽略");
 });
 it("renders free text answers read-only and keeps pending input available", () => {
  const m=message("scope");
  expect(renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />)).toContain("textarea");
  m.kind="user"; m.purpose="human_reply";
  m.content.blocks=[{id:"human",type:"human_reply",outcome:"success",value:"custom answer",question:question("success","custom answer")}];
  const html=renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("custom answer"); expect(html).not.toContain("textarea"); expect(html).not.toContain('checked=""');
 });
 it("shows the original question's selection without loading its reply", () => {
  const m=message("scope");
  m.content.blocks[0].status="success"; m.content.blocks[0].selected_value="small";
  const html=renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("Small"); expect(html).toContain('checked=""');
  expect(html).not.toContain("<button"); expect(html).not.toContain("textarea");
 });
 it("renders a cancelled reply with its question but no invented selection", () => {
  const m=message("cancel"); m.kind="user"; m.purpose="human_reply";
  m.content.blocks=[{id:"human",type:"human_reply",outcome:"dismissed",question:question("dismissed")}];
  const html=renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("Scope"); expect(html).toContain("Small"); expect(html).toContain("已忽略");
  expect(html).not.toContain('checked=""'); expect(html).not.toContain("<button");
 });
 it("does not treat ordinary messages or reply references as Human actions", () => {
  const m=message("ordinary"); m.purpose="output";
  const html=renderToStaticMarkup(<MessageBody message={m} onReply={() => {}} />);
  expect(html).toContain("Choose"); expect(html).not.toContain("<fieldset"); expect(html).not.toContain("<button");
  expect(renderToStaticMarkup(<ReplyReference message={m} />)).toContain('href="#message-input"');
 });
});

function question(status: HumanQuestion["status"], value?: string): HumanQuestion {
 return {id:"human",type:"ask",title:"Scope",prompt:"Choose",status,selected_value:value,deadline:"2030-01-01T00:00:00Z",choices:[{value:"small",label:"Small"},{value:"large",label:"Large"}],allow_other:true};
}

it("renders mixed content blocks without interpreting plain text as Markdown or executing HTML", () => {
 const m=message("content");m.purpose="output";
 m.content.blocks=[{id:"plain",type:"text",content:"**plain**"},{id:"md",type:"markdown",content:"**bold**\n\n`code`\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))"},{id:"tool",type:"tool",name:"Search",status:"completed",content:"Found result"}];
 const html=renderToStaticMarkup(<MessageBody message={m} />);
 expect(html).toContain("**plain**");expect(html).toContain("<strong>bold</strong>");expect(html).toContain("<code>code</code>");
 expect(html).toContain("Search");expect(html).toContain("Found result");expect(html).not.toContain("<script>");expect(html).not.toContain('href="javascript:');
});
