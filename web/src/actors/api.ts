import { requestJSON, type Page } from "../transport/http";
import type { Actor } from "./model";

export async function listActors(signal?: AbortSignal): Promise<Actor[]> {
  const page = await requestJSON<Page<Actor>>("/v1/actors", { signal });
  return page.data;
}
