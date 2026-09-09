import { afterEach, expect, it, vi } from "vitest";
import { findDetailConversation, listConversations } from "./api";

afterEach(() => vi.unstubAllGlobals());

it("finds a reusable actor conversation by parent and actor", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ data: [{ id: "root" }] }))
    .mockResolvedValueOnce(Response.json({ data: [{ id: "child", parent_id: "root/1" }] }))
    .mockResolvedValueOnce(Response.json({ data: [] }));
  vi.stubGlobal("fetch", fetch);
  expect(await listConversations()).toEqual([{ id: "root" }]);
  expect(await findDetailConversation("root/1", "operator", "router")).toEqual({
    id: "child",
    parent_id: "root/1",
  });
  expect(await findDetailConversation("root/1", "operator", "other")).toBeUndefined();
  expect(fetch.mock.calls.map(([path]) => path)).toEqual([
    "/v1/conversations?limit=100",
    "/v1/conversations?parent_id=root%2F1&actor_kind=operator&actor_key=router",
    "/v1/conversations?parent_id=root%2F1&actor_kind=operator&actor_key=other",
  ]);
});
