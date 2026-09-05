import { describe, expect, it } from "vitest";
import { decodeMessageFrame, type Message } from "./api";
import { applyMessageEvent } from "./message";

const message = (id: string): Message => ({
 id, task_id:"task", conversation_id:"work",kind:"harness",key:"same-actor",purpose:"output",
 created_at:"",updated_at:"",revision:1,
 content:{version:"1.0",biz:"chat",meta:{},blocks:[]},
});
const frame = (m:Message,event:unknown) => decodeMessageFrame("data: "+JSON.stringify({message_id:m.id,message:m,event}));
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
  expect(()=>applyMessageEvent([],{...event,messageID:"b"})).toThrow("identity mismatch");
 });
});
