import type { Api } from "../api/client.ts";
import type { GroupEditInput, ProjectGroup } from "../api/types.ts";
import type { HostConnection } from "../platform/index.ts";
import { hostAccessScope } from "./host.ts";

export const isCurrentGroup = (group: ProjectGroup): boolean =>
  group.archivedAt === null && group.state === "current";

export function duplicateGroupTitles(groups: readonly ProjectGroup[]): Set<string> {
  const seen = new Set<string>();
  const duplicates = new Set<string>();
  for (const group of groups) {
    const title = group.title.toLowerCase();
    if (seen.has(title)) duplicates.add(title);
    seen.add(title);
  }
  return duplicates;
}

/** The native bridge routes to the current server, before React can unmount the previous view. */
export async function requestGroupEdit(
  api: Pick<Api, "editProjectGroup">,
  host: HostConnection | undefined,
  scope: string,
  input: GroupEditInput,
  signal: AbortSignal,
  token = "",
) {
  signal.throwIfAborted();
  if (hostAccessScope(host?.current() ?? null) !== scope) throw new Error("The server or account changed.");
  return api.editProjectGroup(input, signal, token);
}
