// @vitest-environment jsdom
import { act, StrictMode, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Message } from "../messages/types";
import { App } from "../app/App";
import { ConversationChat } from "./ConversationChat";
import { useConversationMessages } from "./useConversationMessages";

const actor = { kind: "operator", key: "router" };
const message = (conv: string, id: string, text = id): Message => ({
  id,
  conversation_id: conv,
  status: "completed",
  task_id: "",
  revision: 1,
  source_kind: "user",
  source_key: "user",
  created_at: "2026-09-09T00:00:00Z",
  updated_at: "2026-09-09T00:00:00Z",
  content: { version: "1.1", biz: "chat", meta: {}, blocks: [{ id: "text", type: "text", content: text }] },
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let root: Root;
let container: HTMLDivElement;
let feed: ReturnType<typeof useConversationMessages>;
let rows: Record<string, Message[]>;
let initial: Map<string, ReturnType<typeof deferred<Response>>>;
let writes: Array<{ path: string; response: ReturnType<typeof deferred<Response>> }>;
let streams: Array<{
  conv: string;
  controller: ReadableStreamDefaultController<Uint8Array>;
  cancelled: boolean;
}>;
function Probe({ id }: { id: string }) {
  feed = useConversationMessages(id);
  return (
    <output>
      {feed.messages.map((m) => `${m.id}:${m.content?.blocks[0]?.content ?? "unloaded"}`).join("|")}
    </output>
  );
}
async function render(node: ReactNode) {
  await act(async () => {
    root.render(node);
  });
}
function chat(id: string) {
  return (
    <ConversationChat
      key={id}
      conversation={{ id, name: id, actor_kind: "user", actor_key: "user", created_at: "", updated_at: "" }}
      actor={actor}
      picker={{ actors: [actor], selectedActorID: "operator:router", onSelect() {} }}
    />
  );
}
function emit(index: number, value: unknown) {
  streams[index].controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(value)}\n\n`));
}
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(private callback: IntersectionObserverCallback) {}
      observe(target: Element) {
        this.callback(
          [{ target, isIntersecting: true } as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
      disconnect() {}
    },
  );
  localStorage.clear();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  rows = { A: [message("A", "a")], B: [message("B", "b")] };
  initial = new Map();
  writes = [];
  streams = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      const url = new URL(path, "http://localhost");
      const conv = url.pathname.split("/")[3];
      if (init?.method === "POST") {
        const response = deferred<Response>();
        writes.push({ path, response });
        return response.promise;
      }
      if (url.pathname === "/v1/actors") return Promise.resolve(Response.json({ data: [actor] }));
      if (url.pathname === "/v1/conversations")
        return Promise.resolve(
          Response.json({
            data: url.searchParams.has("parent_id")
              ? []
              : ["A", "B"].map((id) => ({
                  id,
                  name: id,
                  actor_kind: "user",
                  actor_key: "user",
                  created_at: "",
                  updated_at: "",
                })),
          }),
        );
      if (url.pathname.endsWith("/stream")) {
        const stream = { conv, cancelled: false } as (typeof streams)[number];
        const body = new ReadableStream<Uint8Array>({
          start(controller) {
            stream.controller = controller;
            init?.signal?.addEventListener(
              "abort",
              () => {
                if (!stream.cancelled) {
                  stream.cancelled = true;
                  controller.error(new DOMException("aborted", "AbortError"));
                }
              },
              { once: true },
            );
          },
          cancel() {
            stream.cancelled = true;
          },
        });
        streams.push(stream);
        return Promise.resolve(new Response(body));
      }
      if (url.pathname.endsWith("/content")) {
        const id = url.pathname.split("/").at(-2);
        return Promise.resolve(Response.json(rows[conv].find((m) => m.id === id)));
      }
      if (url.searchParams.has("order") && initial.has(conv)) {
        const pending = initial.get(conv)!;
        initial.delete(conv);
        return pending.promise;
      }
      const data = url.searchParams.has("after") ? [] : (rows[conv] ?? []);
      return Promise.resolve(Response.json({ data }));
    }),
  );
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// +case=`Old reads and mutation callbacks cannot enter B or a new visit to A.`
it("isolates history, body and Human results across A -> B -> A", async () => {
  const pending = deferred<Response>();
  initial.set("A", pending);
  await render(<Probe id="A" />);
  const old = feed;
  await render(<Probe id="B" />);
  expect(container.textContent).toBe("b:b");
  await act(async () => {
    pending.resolve(Response.json({ data: [message("A", "late-history")] }));
    old.merge([message("A", "late-body")]);
    old.reply({
      message: message("A", "late-question"),
      reply: message("A", "late-reply"),
      status: "success",
    });
  });
  expect(container.textContent).toBe("b:b");
  expect(streams[0].cancelled).toBe(true);
  await render(<Probe id="A" />);
  await act(async () => old.merge([message("A", "previous-visit")]));
  expect(container.textContent).toBe("a:a");
});
it("survives StrictMode and restores metadata through a stream snapshot", async () => {
  const full = message("A", "a", "restored");
  const { content: _content, ...metadata } = full;
  rows.A = [metadata];
  await render(
    <StrictMode>
      <Probe id="A" />
    </StrictMode>,
  );
  expect(container.textContent).toBe("a:unloaded");
  await act(async () =>
    emit(streams.length - 1, {
      message: full,
      event: { op: "start", seq: 1, stream_id: "a", model: full.content },
    }),
  );
  expect(container.textContent).toBe("a:restored");
});
it("cancels a malformed stream before reconnecting and repairing its snapshot", async () => {
  vi.useFakeTimers();
  await render(<Probe id="A" />);
  await act(async () => streams[0].controller.enqueue(new TextEncoder().encode("data: broken-json\n\n")));
  expect(streams[0].cancelled).toBe(true);
  expect(feed.error).toBeTruthy();
  rows.A = [{ ...message("A", "a", "recovered"), revision: 2 }];
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  expect(streams).toHaveLength(2);
  await act(async () => emit(1, { op: "ping", seq: 0 }));
  expect(container.textContent).toBe("a:recovered");
  expect(feed.error).toBeUndefined();
});
async function send(text: string) {
  const textarea = container.querySelector('textarea[aria-label="Message"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, text);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => {
    container
      .querySelector("form.composer")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}
it("keeps a delayed send response out of a newly mounted conversation", async () => {
  await render(chat("A"));
  await send("A outgoing");
  expect(writes).toHaveLength(1);
  expect(container.textContent).toContain("发送中");
  await render(chat("B"));
  await act(async () => writes[0].response.resolve(Response.json(message("A", "sent", "A outgoing"))));
  expect(container.querySelector(".chat-header h1")?.textContent).toBe("B");
  expect(container.textContent).not.toContain("A outgoing");
  expect(container.querySelector("#message-b")).toBeTruthy();
});
it("keeps a delayed Human reply out of the newly selected conversation", async () => {
  const question = message("A", "question");
  question.content = {
    version: "1.1",
    biz: "chat",
    meta: {},
    blocks: [
      {
        id: "human",
        type: "ask",
        title: "Pick scope",
        prompt: "Choose",
        status: "pending",
        deadline: "2030-01-01T00:00:00Z",
        choices: [{ value: "small", label: "Small" }],
      },
    ],
  };
  rows.A = [question];
  await render(chat("A"));
  await act(async () => (container.querySelector('input[type="radio"]') as HTMLInputElement).click());
  expect(writes).toHaveLength(1);
  expect(writes[0].path).toContain("/A/replies");
  await render(chat("B"));
  await act(async () =>
    writes[0].response.resolve(
      Response.json({ message: question, reply: message("A", "answer"), status: "success" }),
    ),
  );
  expect(container.textContent).not.toContain("Pick scope");
  expect(container.querySelector("#message-answer")).toBeNull();
});

// +case=`Creating and sending can finish after navigation without redirecting the selected view.`
it("does not redirect after a new conversation finishes in the background", async () => {
  await render(<App />);
  await act(async () => (container.querySelector(".new-conversation") as HTMLButtonElement).click());
  await send("new task");
  expect(writes[0].path).toBe("/v1/conversations");
  await act(async () =>
    (container.querySelectorAll(".conversation-list button")[1] as HTMLButtonElement).click(),
  );
  const created = {
    id: "N",
    name: "new task",
    actor_kind: "user",
    actor_key: "user",
    created_at: "",
    updated_at: "",
  };
  await act(async () => writes[0].response.resolve(Response.json(created)));
  expect(writes[1].path).toBe("/v1/conversations/N/messages");
  await act(async () => writes[1].response.resolve(Response.json(message("N", "sent"))));
  expect(container.querySelector(".chat-header h1")?.textContent).toBe("B");
  expect(container.querySelector(".conversation-list")?.textContent).toContain("new task");
});

it("reuses the created conversation when its first message submission fails", async () => {
  await render(<App />);
  await act(async () => (container.querySelector(".new-conversation") as HTMLButtonElement).click());
  await send("new task");
  const created = {
    id: "N",
    name: "new task",
    actor_kind: "user",
    actor_key: "user",
    created_at: "",
    updated_at: "",
  };
  await act(async () => writes[0].response.resolve(Response.json(created)));
  await act(async () =>
    writes[1].response.resolve(Response.json({ error: { message: "unavailable" } }, { status: 503 })),
  );
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("unavailable");
  await send("retry");
  expect(writes.map((write) => write.path)).toEqual([
    "/v1/conversations",
    "/v1/conversations/N/messages",
    "/v1/conversations/N/messages",
  ]);
  rows.N = [message("N", "sent", "retry")];
  await act(async () => writes[2].response.resolve(Response.json(rows.N[0])));
  expect(container.querySelector(".chat-header h1")?.textContent).toBe("new task");
});
