// Connection operations and launch readiness for task and session controls. Read each connection's declarations
// once for the whole app and reuse the status row's live queries.
import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, type ReactNode } from "react";
import { connectionCapabilityOptions, keys, useConnections, useHarnesses, useInvalidateWork, useMachines, useProjects } from "../api/queries.ts";
import { useApi } from "../platform/context.tsx";
import { useConnectionStatuses } from "../harnesses/index.ts";
import type { StartTreeResponse, Task } from "../api/types.ts";
import { statusTargets, type HarnessSummary } from "../lib/connections.ts";
import { connectionBlock, launchBlock, operationReason, readyTreeStarts, type LaunchBlock, type LaunchTarget, type SessionOperation } from "../lib/launch.ts";
import { canStart, canStop } from "../lib/tasks.ts";
import { retiredHint } from "../lib/harnesses.ts";
import { machineSetupState, machineTag, withMachine } from "../lib/machines.ts";
import { POLL } from "../lib/polling.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { Alert, Hint, TextLink, cx, errorText } from "./ui.tsx";

/** The block on launching a task on a connection, or null; `connectionId` and the project name where. */
export type LaunchGate = (t: LaunchTarget) => LaunchBlock | null;

type OperationGate = (connectionId: number, operation: SessionOperation) => LaunchBlock | null;
const CHECKING: LaunchBlock = { kind: "operation", reason: "Checking harness capabilities.", fix: null };
const LaunchGateContext = createContext<{ launch: LaunchGate; operation: OperationGate }>({
  launch: () => CHECKING,
  operation: () => CHECKING,
});

export const useLaunchGate = (): LaunchGate => useContext(LaunchGateContext).launch;
export const useOperationGate = (): OperationGate => useContext(LaunchGateContext).operation;

export function LaunchGateProvider({ children }: { children: ReactNode }) {
  const api = useApi();
  const connections = useConnections();
  const harnesses = useHarnesses();
  const { machines, data } = useMachines();
  const projects = useProjects();
  const loaded = harnesses.loaded && data !== undefined;
  const targets = loaded ? statusTargets(machines, harnesses.connection) : [];
  const reads = useConnectionStatuses(
    targets.filter((t) => machineSetupState(t.machine) === "ready"),
    harnesses.kindName,
    { poll: false },
  );
  const byId = new Map<number, HarnessSummary>(reads.map(({ connection, view }) => [connection.connectionId, view.summary]));
  const configured = connections.data?.connections.filter((c) => c.active && c.configured) ?? [];
  const capabilities = useQueries({
    queries: configured.map((c) => {
      const machine = machines.find((m) => m.id === c.machineId);
      return {
        ...connectionCapabilityOptions(api, c.id),
        enabled: machine !== undefined && machineSetupState(machine) === "ready",
        refetchInterval: POLL.slow,
      };
    }),
  });
  const capsById = new Map(configured.map((c, i) => [c.id, capabilities[i]]));
  const context = (id: number) => {
    const c = harnesses.connection(id);
    const machineId = c?.machineId ?? 0;
    const tag = machineTag(machines, machineId);
    return {
      connection: c,
      machine: machines.find((m) => m.id === machineId),
      live: byId.get(id),
      name: withMachine(harnesses.name(id), tag),
      where: tag === undefined ? "" : `on ${tag}`,
      settings: SETTINGS_PATH.connections,
    };
  };
  const operation: OperationGate = (id, op) => {
    const c = context(id);
    const block = connectionBlock(c, op);
    if (block !== null) return block;
    const caps = capsById.get(id);
    const reason = c.connection?.active === false
      ? retiredHint(c.name)
      : connections.isSuccess && c.connection === undefined
        ? "Connect a harness in Settings."
        : operationReason(op, c.machine === undefined ? undefined : caps?.data, caps?.error ?? connections.error);
    return reason === null ? null : { kind: "operation", reason, fix: null };
  };
  const launch: LaunchGate = (t) =>
    launchBlock(t, {
      ...context(t.connectionId),
      bindings: projects.data?.projects.find((p) => p.id === t.projectId)?.bindings,
    }) ?? operation(t.connectionId, "launch");
  return <LaunchGateContext.Provider value={{ launch, operation }}>{children}</LaunchGateContext.Provider>;
}

export function useStartTask(task: Task | undefined, onStarted?: () => void) {
  const api = useApi();
  const qc = useQueryClient();
  const invalidate = useInvalidateWork();
  const gate = useLaunchGate();
  const block = task === undefined ? CHECKING : gate(task);
  const start = useMutation({
    mutationFn: () => {
      if (block !== null) throw new Error(block.reason);
      if (task === undefined || !canStart(task)) throw new Error("This task cannot start yet.");
      return api.startTask(task.id);
    },
    onSuccess: () => onStarted?.(),
    onSettled: () => Promise.all([invalidate(), qc.invalidateQueries({ queryKey: keys.notifications })]),
  });
  return { start, block };
}

export function useStopTask(task: Task, enabled: boolean) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const gate = useOperationGate();
  const block = enabled ? gate(task.connectionId, "stop") : null;
  const stop = useMutation({
    mutationFn: () => {
      if (block !== null) throw new Error(block.reason);
      if (!canStop(task)) throw new Error("This task has no running session to stop.");
      return api.stopTask(task.id);
    },
    onSettled: invalidate,
  });
  return { stop, block };
}

/** A mixed tree starts only its eligible members. Keep the per-task errors and launch order of Start ready. */
export function useStartReadyTasks(tasks: readonly Task[] | undefined, readError: unknown = null) {
  const api = useApi();
  const qc = useQueryClient();
  const gate = useLaunchGate();
  const invalidate = useInvalidateWork();
  const { tasks: eligible, readyCount, firstBlock } = readyTreeStarts(tasks ?? [], gate);
  const reason = errorText(readError) ?? (eligible.length > 0 ? null : tasks === undefined ? "Checking ready tasks." : firstBlock?.reason ?? "No task is ready");
  const start = useMutation({
    mutationFn: async (): Promise<StartTreeResponse> => {
      if (reason !== null) throw new Error(reason);
      const started: Task[] = [];
      const errors: StartTreeResponse["errors"] = [];
      for (const task of eligible) {
        try {
          started.push(await api.startTask(task.id));
        } catch (error) {
          errors.push({ taskId: task.id, error: errorText(error) ?? "Could not start the task." });
        }
      }
      return { started, errors };
    },
    onSettled: () => Promise.all([invalidate(), qc.invalidateQueries({ queryKey: keys.notifications })]),
  });
  return { start, count: eligible.length, readyCount, reason };
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
 * A disabled button's reason. The wrapper receives hover since the button cannot, and can show the reason on phones.
 */
export function BlockedReason({ reason, className, showOnPhone, children }: { reason: string | undefined; className?: string; showOnPhone?: boolean; children: ReactNode }) {
  if (reason === undefined) return children;
  return (
    <span title={reason} className={cx("inline-flex min-w-0", showOnPhone && "max-md:flex-col max-md:items-start max-md:gap-1", className)}>
      {children}
      {showOnPhone ? <Hint inline className="max-w-[24ch] wrap-anywhere md:hidden">{reason}</Hint> : null}
    </span>
  );
}
