import type { Api } from "../api/client.ts";
import type { Destination, GroupEditInput, GroupWorkspace, ProjectGroup } from "../api/types.ts";
import type { HostConnection } from "../platform/index.ts";
import { hostAccessScope } from "./host.ts";

export const isCurrentGroup = (group: ProjectGroup): boolean =>
  group.archivedAt === null && group.state === "current";

export function groupWorkspaceSummary(group: ProjectGroup): string {
  const [only] = group.members;
  if (only === undefined) return "No workspaces";
  if (group.members.length === 1) return only.path || only.title;
  return `${group.members.length} workspaces`;
}

/** Why the harness won't start a session in this listed workspace, or null. */
export function groupWorkspaceBlocked(workspace: GroupWorkspace): string | null {
  return workspace.catalogState === "listed" && workspace.blockedReason ? workspace.blockedReason : null;
}

/**
 * A listed workspace in this exact group runs project actions, also one its harness won't start in: an action may fix that.
 * A member its harness archived runs them too (restoring it is one); it is never a destination, so the server checks its group.
 */
export function canRunProjectActions(projectId: number, workspace: GroupWorkspace, destinations: readonly Destination[] | undefined): boolean {
  if (workspace.catalogState === "archived") return true;
  return workspace.catalogState === "listed" && destinations?.some((row) =>
    (row.available || Boolean(row.reason)) && row.connectionId === workspace.connectionId && row.workspaceId === workspace.workspaceId &&
    row.groupProjectId === projectId,
  ) === true;
}

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
