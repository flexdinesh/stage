import type { WorkspaceInventory } from "../api/types.ts";
import { dateTime } from "../lib/format.ts";
import { Meta } from "./ui.tsx";

export function WorkspaceInventoryStatus({ data }: { data: WorkspaceInventory }) {
  if (data.inventory === undefined) return null;
  const { state, refreshedAt, lastError } = data.inventory;
  const summary = {
    pending: "Waiting for the first workspace refresh.",
    refreshing: "Refreshing workspaces…",
    ready: `${data.workspaces.length} ${data.workspaces.length === 1 ? "workspace" : "workspaces"} reported.`,
    stale: "Workspace refresh failed.",
    unavailable: "The connection is unavailable.",
    retired: "This connection was retired.",
  }[state];
  return (
    <div role="status" className="flex min-w-0 flex-col gap-1">
      <p className="text-small">{summary}</p>
      {refreshedAt === null ? null : <Meta>
        {state === "ready" ? "Refreshed" : "Last successful refresh"} {dateTime(refreshedAt)}
      </Meta>}
      {lastError === null ? null : <Meta className="wrap-anywhere">{lastError}</Meta>}
    </div>
  );
}
