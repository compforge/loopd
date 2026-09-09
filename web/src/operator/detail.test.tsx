import { expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { OperatorPanel } from "./OperatorPanel";
import { OperatorMessage } from "./OperatorMessage";
import { detailOrganizer } from "./organizer";
import { groupParallelMessages } from "./parallel";
import type { Message } from "../messages/types";

it("resolves user input to its target without requiring an Operator answer", () => {
  expect(
    detailOrganizer({
      source_kind: "user",
      source_key: "user",
      target_kind: "operator",
      target_key: "router",
    }),
  ).toEqual({ kind: "operator", key: "router" });
  expect(
    detailOrganizer({
      source_kind: "user",
      source_key: "user",
      target_kind: "operator",
      target_key: "interaction",
    }),
  ).toEqual({ kind: "operator", key: "interaction" });
});

it("uses the sender for answers and human questions addressed to the user", () => {
  expect(
    detailOrganizer({
      source_kind: "operator",
      source_key: "interaction",
      target_kind: "user",
      target_key: "user",
    }),
  ).toEqual({ kind: "operator", key: "interaction" });
  expect(
    detailOrganizer({
      source_kind: "operator/longhorizon/manager",
      source_key: "run",
      target_kind: "user",
      target_key: "user",
    }),
  ).toEqual({ kind: "operator", key: "longhorizon" });
});

it("prefers an explicit Operator recipient over the author", () => {
  expect(
    detailOrganizer({
      source_kind: "operator",
      source_key: "router",
      target_kind: "operator",
      target_key: "longhorizon",
    }),
  ).toEqual({ kind: "operator", key: "longhorizon" });
});

it("does not invent an Operator for broadcasts from users or direct Harness messages", () => {
  expect(detailOrganizer({ source_kind: "user", source_key: "user" })).toBeUndefined();
  expect(
    detailOrganizer({
      source_kind: "user",
      source_key: "user",
      target_kind: "harness",
      target_key: "agentgo",
    }),
  ).toBeUndefined();
  expect(detailOrganizer({ source_kind: "operator", source_key: "" })).toBeUndefined();
  expect(detailOrganizer()).toBeUndefined();
});

it("shows the selected Operator before a Message or workspace exists", () => {
  const html = renderToStaticMarkup(
    <OperatorPanel selection={{ parentID: "conv", organizer: { kind: "operator", key: "router" } }} />,
  );
  expect(html).toContain("处理详情 · router");
  expect(html).toContain("正在查找 router 的工作会话");
  expect(html).not.toContain("选择一条消息");
});

it("distinguishes no selection from a message with no related Operator", () => {
  expect(renderToStaticMarkup(<OperatorPanel />)).toContain("发送消息或选择历史消息");
  expect(renderToStaticMarkup(<OperatorPanel selection={{ parentID: "conv" }} />)).toContain(
    "这条消息未关联 Operator 工作会话",
  );
});

it("distinguishes custom roles with a shared Run key and renders persisted report errors", () => {
  const messages = ["manager", "executor", "auditor"].map(
    (role): Message => ({
      status: "completed",
      id: role,
      task_id: "task",
      conversation_id: "work",
      source_kind: "operator/longhorizon/harness",
      source_key: `run-uid/${role}`,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:01:00Z",
      content: {
        version: "1.0",
        biz: "chat",
        meta: { title: "Round 2", actor_display_name: role },
        blocks: [{ id: "report", type: "text", content: "Observed artifact", error: "Command timed out" }],
      },
    }),
  );
  expect(groupParallelMessages(messages)[0].columns).toHaveLength(3);
  for (const message of messages) {
    const html = renderToStaticMarkup(<OperatorMessage message={message} index={0} />);
    expect(html).toContain(message.id.toUpperCase());
    expect(detailOrganizer(message)).toEqual({ kind: "operator", key: "longhorizon" });
    expect(html).toContain(`${message.source_kind} / run-uid`);
    expect(html).toContain("Round 2");
    expect(html).toContain("Observed artifact");
    expect(html).toContain("Command timed out");
  }
});

it("renders persisted message output status independently of body content", () => {
  const message: Message = {
    status: "streaming",
    id: "summary",
    conversation_id: "work",
    task_id: "",
    source_kind: "harness",
    source_key: "summary",
    created_at: "",
    updated_at: "",
    content: {
      version: "1.1",
      biz: "chat",
      meta: {},
      blocks: [{ id: "text", type: "text", content: "Already visible" }],
    },
  };
  for (const [status, label] of [
    ["streaming", "生成中"],
    ["failed", "输出失败"],
    ["cancelled", "已取消"],
  ] as const) {
    const html = renderToStaticMarkup(<OperatorMessage message={{ ...message, status }} index={0} />);
    expect(html).toContain(label);
    expect(html).toContain("Already visible");
  }
  expect(
    renderToStaticMarkup(<OperatorMessage message={{ ...message, status: "completed" }} index={0} />),
  ).not.toContain("生成中");
});
