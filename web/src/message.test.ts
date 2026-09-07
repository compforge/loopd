import { describe, expect, it } from "vitest";
import { decodeMessageFrame, type Message } from "./api";
import { applyMessageEvent } from "./message";

const message = (id: string): Message => ({
 status:"streaming", id, task_id:"task", conversation_id:"work",kind:"harness",key:"same-actor",purpose:"output",
 created_at:"",updated_at:"",revision:1,
 content:{version:"1.0",biz:"chat",meta:{},blocks:[]},
});
const frame = (m: Message, event: Record<string, unknown>) => {
 const patch = { ...event, stream_id: m.id };
 return decodeMessageFrame("data: " + JSON.stringify(
  event.op === "start" || event.op === "end" ? { message: m, event: patch } : patch,
 ));
};
describe("message-addressed delivery",()=>{
 it("isolates equal actor, block IDs and seq across outputs and ignores duplicate deltas",()=>{
  const a=message("a"),b=message("b");
  let messages:Message[]=[];
  for(const m of [a,b]){
   messages=applyMessageEvent(messages,frame(m,{op:"start",seq:1,model:m.content}));
   messages=applyMessageEvent(messages,frame(m,{op:"set",seq:2,block:{id:"text",type:"text",content:m.id}}));
  }
  const delta=frame(a,{op:"append",seq:3,mask:"block.content",block:{id:"text",type:"text",content:"!"}});
  messages=applyMessageEvent(messages,delta);
  expect(applyMessageEvent(messages,delta)).toBe(messages);
  expect(messages.map(m=>m.content.blocks[0].content)).toEqual(["a!","b"]);
 });
 it("rejects mismatched envelope identity",()=>{
  const event=frame(message("a"),{op:"start",seq:1,model:message("a").content});
  expect(()=>applyMessageEvent([],{...event,event:{...event.event,stream_id:"b"}})).toThrow("identity mismatch");
 });
 it("keeps metadata for bare deltas and repairs with a newer snapshot", () => {
  const a = {...message("a"), reply_to_id: "question", target_kind: "user", target_key: "alice"};
  let messages = applyMessageEvent([], frame(a, {op: "start", seq: 1, model: a.content}));
  const delta = frame(a, {op: "set", seq: 2, block: {id: "text", type: "text", content: "partial"}});
  expect(delta.message).toBeUndefined();
  messages = applyMessageEvent(messages, delta);
  expect(messages[0]).toMatchObject({kind: a.kind, key: a.key, reply_to_id: "question", target_key: "alice", status: "streaming", revision: 2});
  const repaired = {...a, revision: 5, content: {...a.content, blocks: [{id: "text", type: "text", content: "recovered"}]}};
  messages = applyMessageEvent(messages, frame(repaired, {op: "start", seq: 5, model: repaired.content}));
  expect(applyMessageEvent(messages, delta)).toBe(messages);
  const terminal = {...repaired, status: "completed" as const, revision: 6};
  messages = applyMessageEvent(messages, frame(terminal, {op: "start", seq: 6, model: terminal.content}));
  const end = decodeMessageFrame('data: {"stream_id":"a","op":"end","seq":6}');
  expect(applyMessageEvent(messages, end)).toBe(messages);
  expect(messages[0].status).toBe("completed");
  expect(messages[0].content.blocks[0].content).toBe("recovered");
 });
 it("requires a known message for bare deltas and accepts unaddressed heartbeats", () => {
  const delta = frame(message("missing"), {op: "set", seq: 2, block: {id: "text", type: "text", content: "x"}});
  expect(() => applyMessageEvent([], delta)).toThrow("initial snapshot");
  expect(decodeMessageFrame('data: {"op":"ping","seq":0}').event.stream_id).toBeUndefined();
 });
});

it("ending one message leaves other messages live and accepts later actors", () => {
 const a=message("a"), b=message("b");
 a.status="streaming"; b.status="streaming";
 let messages:Message[]=[];
 for(const m of [a,b]) messages=applyMessageEvent(messages,frame(m,{op:"start",seq:1,model:m.content}));
 messages=applyMessageEvent(messages,frame({...a,status:"completed"},{op:"end",seq:2}));
 messages=applyMessageEvent(messages,frame(b,{op:"set",seq:2,block:{id:"text",type:"text",content:"still speaking"}}));
 expect(messages[0].status).toBe("completed");
 expect(messages[1].status).toBe("streaming");
 const c=message("c"); c.task_id=""; c.status="completed";
 messages=applyMessageEvent(messages,frame(c,{op:"start",seq:1,model:c.content}));
 expect(messages).toHaveLength(3);
 expect(applyMessageEvent(messages,frame(a,{op:"start",seq:1,model:a.content}))).toBe(messages);
});

it("preserves failed and cancelled states on End and history replay", () => {
 for (const status of ["failed", "cancelled"] as const) {
  const current = message("terminal");
  const terminal = {...current, status, revision: 2};
  let messages = applyMessageEvent([current], frame(terminal, {op: "end", seq: 2}));
  expect(messages[0].status).toBe(status);
  expect(messages[0].content.meta.output).toBeUndefined();
  messages = applyMessageEvent(messages, frame(current, {op: "start", seq: 1, model: current.content}));
  expect(messages[0].status).toBe(status);
  const restored = applyMessageEvent([], frame(terminal, {op: "start", seq: 2, model: terminal.content}));
  expect(restored[0].status).toBe(status);
 }
});
