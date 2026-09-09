export interface HumanQuestion {
  id: string;
  type: "ask" | "confirm";
  title: string;
  prompt: string;
  status: "pending" | "success" | "dismissed" | "timeout" | "failure";
  deadline: string;
  choices?: { value: string; label: string; description?: string }[];
  allow_other?: boolean;
  confirm_label?: string;
  decline_label?: string;
  reason?: string;
  selected_value?: string;
}
export function humanStatus(status: HumanQuestion["status"]): string {
  return {
    pending: "等待答复",
    success: "已答复",
    dismissed: "已忽略",
    timeout: "已超时",
    failure: "已结束",
  }[status];
}

// Narrow only loopd's known Human block contract. Other AgentUE block types
// stay open and continue through the ordinary content renderer.
export function isHumanQuestion(value: unknown): value is HumanQuestion {
  if (!value || typeof value !== "object") return false;
  const block = value as Record<string, unknown>;
  return (
    typeof block.id === "string" &&
    (block.type === "ask" || block.type === "confirm") &&
    typeof block.title === "string" &&
    typeof block.prompt === "string" &&
    typeof block.deadline === "string" &&
    ["pending", "success", "dismissed", "timeout", "failure"].includes(String(block.status)) &&
    (block.allow_other === undefined || typeof block.allow_other === "boolean") &&
    ["selected_value", "confirm_label", "decline_label", "reason"].every(
      (key) => block[key] === undefined || typeof block[key] === "string",
    ) &&
    (block.choices === undefined ||
      (Array.isArray(block.choices) &&
        block.choices.every((choice: unknown) => {
          if (!choice || typeof choice !== "object") return false;
          const item = choice as Record<string, unknown>;
          return (
            typeof item.value === "string" &&
            typeof item.label === "string" &&
            (item.description === undefined || typeof item.description === "string")
          );
        })))
  );
}
