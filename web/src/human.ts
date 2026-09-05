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
}
export function humanStatus(status: HumanQuestion["status"]): string {
  return { pending: "等待答复", success: "已答复", dismissed: "已忽略", timeout: "已超时", failure: "已结束" }[status];
}
