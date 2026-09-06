import { expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { DetailPanel, DetailMessage, detailOrganizer } from "./DetailPanel";
import { groupParallelMessages } from "./parallel";
import type { Message } from "./api";

it("resolves user input to its target without requiring an Operator answer", () => {
  expect(detailOrganizer({ kind: "user", key: "user", target_kind: "operator", target_key: "router" }))
    .toEqual({ kind: "operator", key: "router" });
  expect(detailOrganizer({ kind: "user", key: "user", target_kind: "operator", target_key: "interaction" }))
    .toEqual({ kind: "operator", key: "interaction" });
});

it("uses the sender for answers and human questions addressed to the user", () => {
  expect(detailOrganizer({ kind: "operator", key: "interaction", target_kind: "user", target_key: "user" }))
    .toEqual({ kind: "operator", key: "interaction" });
  expect(detailOrganizer({ kind: "operator/longhorizon/manager", key: "run", target_kind: "user", target_key: "user" }))
    .toEqual({ kind: "operator", key: "longhorizon" });
});

it("prefers an explicit Operator recipient over the author", () => {
  expect(detailOrganizer({ kind: "operator", key: "router", target_kind: "operator", target_key: "longhorizon" }))
    .toEqual({ kind: "operator", key: "longhorizon" });
});

it("does not invent an Operator for broadcasts from users or direct Harness messages", () => {
  expect(detailOrganizer({ kind: "user", key: "user" })).toBeUndefined();
  expect(detailOrganizer({ kind: "user", key: "user", target_kind: "harness", target_key: "agentgo" })).toBeUndefined();
  expect(detailOrganizer({ kind: "operator", key: "" })).toBeUndefined();
  expect(detailOrganizer()).toBeUndefined();
});

it("shows the selected Operator before a Message or workspace exists", () => {
  const html = renderToStaticMarkup(<DetailPanel selection={{ parentID: "conv", organizer: { kind: "operator", key: "router" } }} running={false} />);
  expect(html).toContain("处理详情 · router");
  expect(html).toContain("正在查找 router 的工作会话");
  expect(html).not.toContain("选择一条消息");
});

it("distinguishes no selection from a message with no related Operator", () => {
  expect(renderToStaticMarkup(<DetailPanel running={false} />)).toContain("发送消息或选择历史消息");
  expect(renderToStaticMarkup(<DetailPanel selection={{ parentID: "conv" }} running={false} />))
    .toContain("这条消息未关联 Operator 工作会话");
});

it("distinguishes custom roles with a shared Run key and renders persisted report errors", () => {
  const messages = ["manager", "executor", "auditor"].map((role): Message => ({
    status: "completed", id: role, task_id: "task", conversation_id: "work", kind: `operator/longhorizon/${role}`, key: "run-uid", purpose: "output",
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

it("renders persisted message output status independently of body content", () => {
  const message: Message = { status: "streaming", id: "summary", conversation_id: "work", task_id: "", kind: "harness", key: "summary", created_at: "", updated_at: "", content: {version: "1.1", biz: "chat", meta: {}, blocks: [{id: "text", type: "text", content: "Already visible"}]} };
  for (const [status, label] of [["streaming", "生成中"], ["failed", "输出失败"], ["cancelled", "已取消"]] as const) {
    const html = renderToStaticMarkup(<DetailMessage message={{...message, status}} index={0} />);
    expect(html).toContain(label);
    expect(html).toContain("Already visible");
  }
  expect(renderToStaticMarkup(<DetailMessage message={{...message, status: "completed"}} index={0} />)).not.toContain("生成中");
});
