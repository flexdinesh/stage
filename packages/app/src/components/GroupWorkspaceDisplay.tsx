import type { GroupWorkspace, ProjectGroup } from "../api/types.ts";
import { Chip, Meta, type ChipTone } from "./ui.tsx";
import type { WorkspaceConnections } from "./WorkspaceDisplay.tsx";

export function groupWorkspaceLabel(workspace: GroupWorkspace, connections: WorkspaceConnections): string {
  const { harnesses, placement } = connections;
  const site = placement(workspace.connectionId, workspace);
  const duplicate = harnesses.list.some((other) =>
    other.id !== workspace.connectionId && other.machineId === workspace.machineId &&
    placement(other.id).harness === site.harness);
  return [site.machineName, site.harness, ...(duplicate ? [`#${workspace.connectionId}`] : [])].join(" · ");
}

export function groupWorkspaceLocation(workspace: GroupWorkspace): string {
  return workspace.path || workspace.workspaceId;
}

export function groupStateLabel(group: ProjectGroup): string | null {
  if (group.archivedAt !== null) return "Archived";
  if (group.state === "empty") return "No workspaces";
  if (group.state === "retired") return "Retired workspaces";
  return null;
}

export function GroupSummary({ group, connections, disambiguate = false }: {
  group: ProjectGroup;
  connections: WorkspaceConnections;
  disambiguate?: boolean;
}) {
  const distinct = new Map(group.members.map((workspace) => [workspace.connectionId, workspace]));
  const labels = Array.from(distinct.values(), (workspace) => groupWorkspaceLabel(workspace, connections));
  const first = group.members[0];
  const state = groupStateLabel(group);
  return (
    <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
      {labels.length === 0 ? null : <Meta className="wrap-anywhere">{labels.slice(0, 2).join(" / ")}</Meta>}
      {labels.length <= 2 ? null : <Meta>+{labels.length - 2} more</Meta>}
      {group.members.length < 2 ? null : <Meta>{group.members.length} workspaces</Meta>}
      {first !== undefined && (disambiguate || group.members.length === 1)
        ? <Meta className="wrap-anywhere">{first.path || (disambiguate ? first.workspaceId : first.title)}</Meta> : null}
      {first !== undefined && state === null && group.members.length === 1
        ? <GroupWorkspaceState workspace={first} connections={connections} /> : null}
      {state === null ? null : <Chip tone="idle">{state}</Chip>}
    </span>
  );
}

export function GroupWorkspaceState({ workspace, connections }: {
  workspace: GroupWorkspace;
  connections: WorkspaceConnections;
}) {
  const site = connections.placement(workspace.connectionId, workspace);
  const state = workspace.catalogState;
  let label = "Listed";
  let tone: ChipTone = "idle";
  if (state === "retired" || site.retired) label = "Retired connection";
  else if (site.machineStatus !== null) label = `Machine ${site.machineStatus.toLowerCase()}`;
  else if (site.disconnected) label = "Disconnected";
  else if (state === "archived") label = "Archived by harness";
  else if (state === "missing") label = "Missing from catalog";
  else if (site.live) tone = "ok";
  return <Chip tone={tone} dot>{label}</Chip>;
}
