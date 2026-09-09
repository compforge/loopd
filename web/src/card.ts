import type { Message } from "./api";
import type { HumanQuestion } from "./human";

export interface HumanCard {
  type: "ask" | "confirm";
  mode: "request" | "reply";
  question_id: string;
  question: HumanQuestion;
  selected_value?: string;
  editable: boolean;
}

// Cards are a local rendering of one persisted message, never a relation lookup.
// Only typed Human messages are interactive; ordinary content remains content.
export function humanCard(message: Message): HumanCard | undefined {
  const block = message.content?.blocks[0];
  if (!block) return undefined;
  if (block.type === "ask" || block.type === "confirm") {
    const question = block as unknown as HumanQuestion;
    return {
      type: question.type, mode: "request", question_id: message.id, question,
      selected_value: question.selected_value, editable: question.status === "pending",
    };
  }
  if (block.type === "human_reply" && message.reply_to_id) {
    const question = block.question as HumanQuestion | undefined;
    if (!question || (question.type !== "ask" && question.type !== "confirm")) return undefined;
    return {
      type: question.type, mode: "reply", question_id: message.reply_to_id, question,
      selected_value: question.selected_value, editable: false,
    };
  }
}
