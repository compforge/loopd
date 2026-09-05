import type { Message } from "./api";

export interface DetailGroup {
  columns: Message[][];
}

/** @spec 相交的消息区间分列；时间只决定并行关系，不决定卡片高度。 */
export function groupParallelMessages(messages: Message[]): DetailGroup[] {
  const intervals = messages.map((message) => {
    const start = Date.parse(message.created_at);
    const updated = Date.parse(message.updated_at);
    return { message, start, end: Math.max(start, Number.isFinite(updated) ? updated : start) };
  }).sort((a, b) => {
    const order = (Number.isFinite(a.start) ? a.start : Infinity) - (Number.isFinite(b.start) ? b.start : Infinity);
    return (Number.isNaN(order) ? 0 : order) || a.message.id.localeCompare(b.message.id);
  });
  const groups: DetailGroup[] = [];
  let end = -Infinity;
  let actors = new Map<string, number>();
  for (const interval of intervals) {
    // Missing dates cannot establish concurrency. Render them independently.
    if (!Number.isFinite(interval.start)) {
      groups.push({ columns: [[interval.message]] });
      continue;
    }
    if (!groups.length || interval.start > end) {
      groups.push({ columns: [] });
      actors = new Map();
    }
    const group = groups[groups.length - 1];
    // Columns belong to actors within this overlap group, not the whole
    // conversation. A later independent group starts at the left again.
    const actor = JSON.stringify([interval.message.kind, interval.message.key]);
    let column = actors.get(actor);
    if (column === undefined) {
      column = actors.size;
      actors.set(actor, column);
      group.columns.push([]);
    }
    group.columns[column].push(interval.message);
    end = Math.max(end, interval.end);
  }
  return groups;
}
