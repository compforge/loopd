import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { readActiveTasks, removeActiveTask, writeActiveTask } from "./streams";

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  });
});
afterEach(() => vi.unstubAllGlobals());

it("keeps independent replay positions when input arrives during another Chat", () => {
  writeActiveTask("conv", { taskID: "first", lastEventID: "1-0" });
  writeActiveTask("conv", { taskID: "followup", lastEventID: "2-0" });
  writeActiveTask("conv", { taskID: "first", lastEventID: "3-0" });
  expect(readActiveTasks().conv).toHaveLength(2);
  expect(readActiveTasks().conv.find((item) => item.taskID === "first")?.lastEventID).toBe("3-0");
  removeActiveTask("conv", "followup");
  expect(readActiveTasks().conv).toEqual([{ taskID: "first", lastEventID: "3-0" }]);
  removeActiveTask("conv", "first");
  expect(readActiveTasks()).toEqual({});
});

it("restores a previously saved single delivery", () => {
  localStorage.setItem("loopd.active-tasks", JSON.stringify({ conv: { taskID: "first", lastEventID: "1-0" } }));
  writeActiveTask("conv", { taskID: "second", lastEventID: "" });
  expect(readActiveTasks().conv.map((item) => item.taskID)).toEqual(["first", "second"]);
});

