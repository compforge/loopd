import Markdown from "react-markdown";
import type { BaseBlock } from "@compforge/agentue/ui";
import { humanStatus, isHumanQuestion } from "./human";

interface ResultBlock extends BaseBlock {
  type: "result";
  format: "text" | "json";
  content: unknown;
}
function isResult(block: BaseBlock): block is ResultBlock {
  return (
    block.type === "result" &&
    (block.format === "json" || (block.format === "text" && typeof block.content === "string"))
  );
}

// A small explicit dispatch is sufficient for the current block set. Unknown
// types keep their visible content without being interpreted as Human actions.
export function BlockBody({ block }: { block: BaseBlock }) {
  let body;
  if (isHumanQuestion(block)) {
    body = (
      <>
        <strong>{block.title}</strong>
        <p>{block.prompt}</p>
        <small>{humanStatus(block.status)}</small>
      </>
    );
  } else if (isResult(block) && block.format === "json") {
    body = <pre className="result-json">{JSON.stringify(block.content, null, 2)}</pre>;
  } else if (block.type === "human_reply") {
    body = <p>{block.outcome === "dismissed" ? "已忽略" : String(block.value ?? "已答复")}</p>;
  } else {
    body = (
      <>
        {block.type === "tool" && (
          <div className="detail-card-subtitle">
            {String(block.name ?? "TOOL")} {String(block.status ?? "")}
          </div>
        )}
        {typeof block.content === "string" ? (
          block.type === "markdown" ? (
            <div className="markdown-content">
              <Markdown>{block.content}</Markdown>
            </div>
          ) : (
            <p>{block.content}</p>
          )
        ) : (
          block.content !== undefined && <pre>{JSON.stringify(block.content, null, 2)}</pre>
        )}
      </>
    );
  }
  return (
    <div>
      {body}
      {typeof block.error === "string" && block.error && <p role="alert">{block.error}</p>}
    </div>
  );
}
