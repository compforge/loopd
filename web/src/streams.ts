import type { Actor } from "./api";

const activeTasksKey = "loopd.active-tasks";

export interface StoredTask {
  taskID: string;
  lastEventID: string;
  target?: Pick<Actor, "kind" | "key">;
}

// A conversation can have multiple in-flight Chat deliveries. Completing one
// must not erase the replay position of another, even for the same Operator.
export function readActiveTasks(): Record<string, StoredTask[]> {
  try {
    const stored = JSON.parse(localStorage.getItem(activeTasksKey) ?? "{}") as Record<string, StoredTask | StoredTask[]>;
    return Object.fromEntries(Object.entries(stored).map(([id, tasks]) => [id, Array.isArray(tasks) ? tasks : [tasks]]));
  } catch { return {}; }
}

export function writeActiveTask(conversationID: string, task: StoredTask) {
  const tasks = readActiveTasks();
  tasks[conversationID] = [...(tasks[conversationID] ?? []).filter((item) => item.taskID !== task.taskID), task];
  localStorage.setItem(activeTasksKey, JSON.stringify(tasks));
}

export function removeActiveTask(conversationID: string, taskID: string) {
  const tasks = readActiveTasks();
  tasks[conversationID] = (tasks[conversationID] ?? []).filter((item) => item.taskID !== taskID);
  if (tasks[conversationID].length === 0) delete tasks[conversationID];
  localStorage.setItem(activeTasksKey, JSON.stringify(tasks));
}

