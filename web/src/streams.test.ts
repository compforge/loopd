import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { readSubscriptions, writeSubscription } from "./streams";

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  });
});
afterEach(() => vi.unstubAllGlobals());

it("replaces the connection identity without accumulating per-input streams", () => {
  writeSubscription("conv", { taskID: "first", lastEventID: "1-0" });
  writeSubscription("other", { taskID: "other", lastEventID: "" });
  writeSubscription("conv", { taskID: "followup", lastEventID: "2-0" });
  expect(readSubscriptions()).toEqual({
    conv: { taskID: "followup", lastEventID: "2-0" },
    other: { taskID: "other", lastEventID: "" },
  });
});
