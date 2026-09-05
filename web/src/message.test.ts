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

it("ending one message leaves other messages live and accepts later actors", () => {
 const a=message("a"), b=message("b");
 a.content.meta.output={ended:false}; b.content.meta.output={ended:false};
 let messages:Message[]=[];
 for(const m of [a,b]) messages=applyMessageEvent(messages,frame(m,{op:"start",seq:1,model:m.content}));
 messages=applyMessageEvent(messages,frame(a,{op:"end",seq:2}));
 messages=applyMessageEvent(messages,frame(b,{op:"set",seq:2,block:{id:"text",type:"text",content:"still speaking"}}));
 expect(messages[0].content.meta.output).toEqual({ended:true});
 expect(messages[1].content.meta.output).toEqual({ended:false});
 const c=message("c"); c.task_id=""; c.content.meta.output={ended:true};
 messages=applyMessageEvent(messages,frame(c,{op:"start",seq:1,model:c.content}));
 expect(messages).toHaveLength(3);
 expect(applyMessageEvent(messages,frame(a,{op:"start",seq:1,model:a.content}))).toBe(messages);
});
