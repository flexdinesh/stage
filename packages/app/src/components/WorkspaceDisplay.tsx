import type { ReactNode } from "react";
import { useHarnesses, useMachines } from "../api/queries.ts";
import type { ProjectBinding } from "../api/types.ts";
import { machineStatusLabel } from "../lib/machines.ts";
import { currentBindings, orderedWorkspaces, primaryWorkspace, workspaceLocation } from "../lib/projects.ts";
import { HarnessFacts } from "./HarnessPresentation.tsx";
import { Chip, CopyButton, ErrorText, Meta, type ChipTone } from "./ui.tsx";

export function useWorkspaceConnections() {
  const harnesses = useHarnesses();
  const machines = useMachines();
  return {
    harnesses,
    machines,
    placement(connectionId: number, binding?: ProjectBinding) {
      const connection = harnesses.connection(connectionId);
      const machineId = binding?.machineId ?? connection?.machineId;
      const machine = machines.machines.find((m) => m.id === machineId);
      const kind = binding?.harness ?? connection?.harness;
      const label = binding?.connectionLabel || connection?.label;
      const name = kind === undefined ? `Connection #${connectionId}` : harnesses.kindName(kind);
      const machineName = machine?.label ?? (machineId === undefined ? "Unknown machine" : `Machine #${machineId}`);
      const machineStatus = machine === undefined || machine.online ? null : machineStatusLabel(machine.status);
      const retired = binding?.connectionActive === false || connection?.active === false;
      const disconnected = connection?.active === true && !connection.configured;
      const live = connection?.active === true && connection.configured && machine?.online === true;
      return {
        machineName, machineStatus, machineReason: machine?.statusReason, retired, disconnected, live,
        harness: label && label !== name ? `${name} · ${label}` : name,
        hint: kind === undefined ? undefined : harnesses.descriptor(kind)?.discoveryHint,
      };
    },
  };
}

type Placement = ReturnType<ReturnType<typeof useWorkspaceConnections>["placement"]>;
export type WorkspaceConnections = ReturnType<typeof useWorkspaceConnections>;

function workspaceState(binding: ProjectBinding, site: Placement): { text: string; tone: ChipTone } {
  if (site.retired) return { text: "Retired connection", tone: "idle" };
  if (site.machineStatus !== null) return { text: `Machine ${site.machineStatus.toLowerCase()}`, tone: "idle" };
  if (site.disconnected) return { text: "Disconnected", tone: "idle" };
  if (binding.status === "changed") return { text: "Path changed", tone: "need" };
  if (binding.status === "missing") return { text: "Unavailable", tone: "bad" };
  return { text: "Available", tone: "ok" };
}

/** Compact project placement and the expanded list use the same bindings and status labels. */
export function WorkspaceDisplay({
  bindings, connectionId, renderActions, connections,
}: {
  bindings: readonly ProjectBinding[];
  connectionId?: number | undefined;
  connections: WorkspaceConnections;
  renderActions?: (binding: ProjectBinding, site: Placement) => ReactNode;
}) {
  const { machines, harnesses, placement } = connections;
  const ordered = orderedWorkspaces(currentBindings(bindings));
  const summary = primaryWorkspace(ordered, connectionId);
  const more = ordered.length - 1;
  if (renderActions === undefined) {
    if (summary === undefined) return <Meta>Workspace not bound</Meta>;
    const site = placement(summary.connectionId, summary);
    const state = workspaceState(summary, site);
    const location = workspaceLocation(summary);
    return (
      <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        <Meta className="wrap-anywhere">{site.machineName} · {site.harness} · {location}</Meta>
        {summary.primary && ordered.length > 1 ? <Meta>Primary</Meta> : null}
        <Chip tone={state.tone}>{state.text}</Chip>
        {more === 0 ? null : <Meta>+{more} {more === 1 ? "workspace" : "workspaces"}</Meta>}
      </span>
    );
  }
  const groups = new Map<number, ProjectBinding[]>();
  for (const binding of ordered) {
    const group = groups.get(binding.machineId);
    if (group === undefined) groups.set(binding.machineId, [binding]);
    else group.push(binding);
  }
  return (
    <div className="min-w-0">
      <ErrorText error={machines.error ?? harnesses.error} />
      {ordered.length === 0 ? (
        <p className="px-3 py-3 text-small text-muted">
          {!harnesses.loaded && harnesses.error === null ? "Loading connections…" :
            harnesses.error !== null ? "Connection status is unavailable." :
              harnesses.connectedList.length === 0 ? "Connect a harness to bind a workspace." : "No workspaces bound. Bind one to launch tasks."}
        </p>
      ) : Array.from(groups, ([machineId, group]) => {
        const first = group[0];
        if (first === undefined) return null;
        const site = placement(first.connectionId, first);
        return (
          <section key={machineId} aria-label={site.machineName} className="border-t border-line first:border-0">
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-2">
              <Meta>Machine</Meta>
              <h3 className="wrap-anywhere text-small font-semibold">{site.machineName}</h3>
              {site.machineStatus === null ? null : <Chip tone="idle">{site.machineStatus}</Chip>}
              {site.machineReason ? <Meta className="wrap-anywhere">{site.machineReason}</Meta> : null}
            </div>
            <ul className="divide-y divide-line">
              {group.map((binding) => {
                const target = placement(binding.connectionId, binding);
                const state = workspaceState(binding, target);
                const location = workspaceLocation(binding);
                const copy = binding.workspace || binding.externalProjectId;
                return (
                  <li key={binding.id} className="flex min-w-0 flex-col gap-2 px-3 py-3">
                    <dl className="grid min-w-0 gap-x-4 gap-y-2 md:grid-cols-[minmax(0,1fr)_minmax(0,2.5fr)_minmax(0,1fr)]">
                      <div className="min-w-0">
                        <dt><Meta>Harness</Meta></dt>
                        <dd className="wrap-anywhere text-small">{target.harness}</dd>
                      </div>
                      <div className="min-w-0">
                        <dt><Meta>{binding.workspace ? "Path" : "External workspace"}</Meta></dt>
                        <dd className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                          <code className="min-w-0 wrap-anywhere text-meta">{location}</code>
                          <CopyButton text={copy} label="Copy" accessibleLabel={`Copy ${copy} on ${target.machineName}, ${target.harness}`} variant="ghost" />
                        </dd>
                        {!binding.workspace && binding.externalProjectId !== location ? <dd><Meta className="wrap-anywhere">{binding.externalProjectId}</Meta></dd> : null}
                      </div>
                      <div className="min-w-0">
                        <dt><Meta>State</Meta></dt>
                        <dd className="flex flex-wrap items-center gap-2">
                          <Chip tone={state.tone} dot>{state.text}</Chip>
                          {binding.primary ? <Meta>Primary</Meta> : null}
                        </dd>
                      </div>
                    </dl>
                    <HarnessFacts facts={binding.facts} />
                    {renderActions(binding, target)}
                  </li>
                );
              })}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
