// Whether a task's connection can launch it now (lib/launch.ts launchBlock), for every Start, state chip and the task
// form. One provider reads connections, machines, projects and each connection's live status (the status row's
// queries, without a second poll), so a long list does not multiply status reads.
import { createContext, useContext, type ReactNode } from "react";
import { useConnectionStatuses, useHarnesses, useMachines, useProjects } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { harnessSummary, openCodeSummary, statusRead, statusTargets, t3Summary, type HarnessSummary } from "../lib/connections.ts";
import { launchBlock, type LaunchBlock } from "../lib/launch.ts";
import { machineSetupState, machineTag, withMachine } from "../lib/machines.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { Alert, TextLink, cx } from "./ui.tsx";

/** The block on launching a task on a connection, or null; `connectionId` and the project name where. */
export type LaunchGate = (t: Pick<Task, "connectionId" | "projectId" | "projectTitle">) => LaunchBlock | null;

const OPEN: LaunchGate = () => null;
const LaunchGateContext = createContext<LaunchGate>(OPEN);

/** Outside the provider nothing is blocked: the server still refuses a launch it cannot make. */
export const useLaunchGate = (): LaunchGate => useContext(LaunchGateContext);

export function LaunchGateProvider({ children }: { children: ReactNode }) {
  const harnesses = useHarnesses();
  const { machines, data } = useMachines();
  const projects = useProjects();
  const loaded = harnesses.loaded && data !== undefined;
  const targets = loaded ? statusTargets(machines, harnesses.connection) : [];
  const reads = useConnectionStatuses(
    targets.filter((t) => machineSetupState(t.machine) === "ready"),
    { poll: false },
  );
  const live: (HarnessSummary & { id: number })[] = [
    ...reads.t3.map(({ connection, query }) => ({ id: connection.connectionId, ...t3Summary(statusRead(query)) })),
    ...reads.openCode.map(({ connection, query }) => ({ id: connection.connectionId, ...openCodeSummary(statusRead(query)) })),
    ...reads.other.map(({ connection, query }) => ({
      id: connection.connectionId,
      ...harnessSummary(statusRead(query), connection.harness, harnesses.kindName(connection.harness)),
    })),
  ];
  const byId = new Map(live.map((l) => [l.id, l]));
  const gate: LaunchGate = (t) => {
    const c = harnesses.connection(t.connectionId);
    const machineId = c?.machineId ?? 0;
    const tag = machineTag(machines, machineId);
    return launchBlock(t, {
      connection: c,
      machine: machines.find((m) => m.id === machineId),
      live: byId.get(t.connectionId),
      bindings: projects.data?.projects.find((p) => p.id === t.projectId)?.bindings,
      name: withMachine(harnesses.name(t.connectionId), tag),
      where: tag === undefined ? "" : `on ${tag}`,
      settings: SETTINGS_PATH.connections,
    });
  };
  return <LaunchGateContext.Provider value={gate}>{children}</LaunchGateContext.Provider>;
}

/** A blocked Start's notice: the reason, then the link that fixes it as the Alert's action; `after` follows the reason. */
export function LaunchBlockAlert({ block, tone, after }: { block: LaunchBlock; tone?: "need" | "plain"; after?: string }) {
  return (
    <Alert
      {...(tone === undefined ? {} : { tone })}
      {...(block.fix === null
        ? {}
        : {
            actions: (
              <TextLink to={block.fix.to} variant="rest">
                {block.fix.label}
              </TextLink>
            ),
          })}
    >
      {block.reason}
      {after === undefined ? null : ` ${after}`}
    </Alert>
  );
}

/**
 * A disabled button's reason as a hover title: disabled buttons take no pointer events, so the title sits on a wrapper.
 * Without a reason it renders the button alone.
 */
export function BlockedReason({ reason, className, children }: { reason: string | undefined; className?: string; children: ReactNode }) {
  if (reason === undefined) return children;
  return (
    <span title={reason} className={cx("inline-flex", className)}>
      {children}
    </span>
  );
}
