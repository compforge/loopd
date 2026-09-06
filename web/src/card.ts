import type { HumanQuestion } from "./human";

export type MessageCard = { type: "content"; editable: false } | {
  type: "ask" | "confirm";
  mode: "request" | "reply";
  question_id: string;
  question: HumanQuestion;
  selected_value?: string;
  reply_id?: string;
  editable: boolean;
};
export type HumanCard = Extract<MessageCard, { type: "ask" | "confirm" }>;
