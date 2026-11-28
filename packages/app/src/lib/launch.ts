import type { Capabilities, Destination, HarnessConnection, Machine, Task } from "../api/types.ts";
import type { HarnessSummary } from "./connections.ts";
import { relativeTime } from "./format.ts";
import { machineSetupState } from "./machines.ts";
import { canStart } from "./tasks.ts";

/** Copy for the task detail banner of a launch Stage has not confirmed; `name` is the harness's name. */
export interface LaunchNotice {
  title: string;
  /** The session the harness returned ("title · state"); null when none was seen. */
  session: string | null;
  /** Why the last check failed; null when none failed yet. */
  reason: string | null;
  /** Failed checks, the next retry and when the launch started; null when nothing failed yet. */
  meta: string | null;
  body: string;
}

export function launchNotice(t: Pick<Task, "launch" | "stateSince">, name: string, now: number = Date.now()): LaunchNotice {
  const launch = t.launch;
  const seen = launch?.session ?? null;
  const reason = launch === undefined || launch.reason === "" ? null : launch.reason;
  const meta = launch === undefined || launch.retryCount === 0 ? null : retryMeta(launch.retryCount, launch.retryAt, t.stateSince, now);
  if (seen === null) {
    return { title: "Launch unconfirmed", session: null, reason, meta, body: "Stage is checking the existing attempt. New work stays blocked until its outcome is known." };
  }
  return {
    title: `Launched in ${name}; Stage can't record it`,
    session: `${seen.title === "" ? seen.externalSessionId : seen.title} · ${seen.state.replaceAll("_", " ")}`,
    reason: reason === null ? null : `Recording failed: ${reason}`,
    meta,
    body: "New work on this task stays blocked until Stage records the session. Stage keeps retrying.",
  };
}

function retryMeta(failed: number, retryAt: string | null, since: string | null, now: number): string {
  const parts = [`${failed} failed ${failed === 1 ? "check" : "checks"}`];
  if (retryAt !== null) parts.push(new Date(retryAt).getTime() > now ? `next retry ${relativeTime(retryAt, now)}` : "retrying now");
  if (since !== null) parts.push(`started ${relativeTime(since, now)}`);
  return parts.join(" · ");
}

/** Why a task's connection cannot launch it right now. */
export type LaunchBlockKind = "disconnected" | "offline" | "negotiating" | "needs_update" | "unreachable" | "unbound" | "operation";

export type SessionOperation = "launch" | "stop" | "adopt" | "send";

const operationTarget = { launch: "start this task", stop: "stop this task", adopt: "track sessions", send: "send messages" };
const operationName = { launch: "starting tasks", stop: "stopping tasks", adopt: "tracking sessions", send: "sending messages" };

/** An operation is usable only when the connection has explicitly advertised it. */
export function operationReason(op: SessionOperation, capabilities: Capabilities | undefined, error: Error | null = null): string | null {
  const declaration = capabilities?.operations?.[op];
  if (error !== null) return error.message;
  if (declaration?.available === true) return null;
  if (capabilities === undefined) return `Checking whether this connection supports ${operationName[op]}.`;
  return declaration?.reason?.trim() || `This connection does not support ${operationName[op]}.`;
}

/** A task Start refuses before the request: why (a sentence for an Alert or a tooltip) and where the user fixes it. */
export interface LaunchBlock {
  kind: LaunchBlockKind;
  reason: string;
  /** The page that fixes it; null when waiting for the machine to connect. */
  fix: { label: string; to: string } | null;
}

/** Connection status used to gate session operations. */
interface ConnectionContext {
  connection: Pick<HarnessConnection, "active" | "configured"> | undefined;
  machine: Pick<Machine, "kind" | "status" | "label" | "statusReason"> | undefined;
  /** Its live status on its machine; undefined until read (checking blocks nothing). */
  live: Pick<HarnessSummary, "tone" | "label"> | undefined;
  /** The connection's display name ("T3", or "T3 · desk" with two or more machines). */
  name: string;
  /** "on desk" with two or more machines, else "". */
  where: string;
  /** Where Settings fixes a connection. */
  settings: string;
}

export interface LaunchContext extends ConnectionContext {
  destinations?: readonly Destination[];
  destinationError?: boolean;
}

/**
 * A connection problem that blocks the requested operation. Callers handle retired connections separately.
 */
export function connectionBlock(c: ConnectionContext, op: SessionOperation): LaunchBlock | null {
  const { connection, machine, live, name, where, settings } = c;
  const target = operationTarget[op];
  const action = target.charAt(0).toUpperCase() + target.slice(1);
  if (connection === undefined || !connection.active) return null;
  if (!connection.configured) {
    return { kind: "disconnected", reason: `${name} is disconnected. Connect it to ${target}.`, fix: { label: "Connect in Settings", to: settings } };
  }
  if (machine !== undefined) {
    const state = machineSetupState(machine);
    switch (state) {
      case "offline":
        return { kind: "offline", reason: `${machine.label} is offline. ${action} once it is back.`, fix: null };
      case "negotiating":
        return { kind: state, reason: `${machine.label} is connecting. ${action} when it is online.`, fix: null };
      case "needs_update":
        return { kind: state, reason: machine.statusReason || (op === "launch" ? "A Stage update is required before this task can start." : `Update Stage to ${target}.`), fix: { label: "Open Settings", to: settings } };
      case "ready":
        break;
    }
  }
  if (live !== undefined && (live.tone === "bad" || live.tone === "need")) {
    const reason = op === "launch" ? "this task cannot start" : `Stage cannot ${target}`;
    return { kind: "unreachable", reason: `${live.label}${where === "" ? "" : ` ${where}`}, so ${reason}.`, fix: { label: "Fix in Settings", to: settings } };
  }
  return null;
}

export type LaunchTarget = Pick<Task, "connectionId" | "workspaceId">;

export function launchBlock(t: LaunchTarget, c: LaunchContext): LaunchBlock | null {
  const block = connectionBlock(c, "launch");
  if (block !== null) return block;
  if (!t.workspaceId) return { kind: "unbound", reason: "Choose a workspace in Edit before starting.", fix: null };
  if (c.destinations === undefined) {
    const reason = c.destinationError ? "Stage could not check the saved workspace." : "Checking the saved workspace…";
    return { kind: "unbound", reason, fix: null };
  }
  const row = c.destinations.find((d) => d.connectionId === t.connectionId && d.workspaceId === t.workspaceId);
  return row?.available === true ? null : {
    kind: "unbound",
    reason: row?.reason || "The saved workspace is unavailable. Choose another workspace in Edit.",
    fix: null,
  };
}

/** Start ready includes ready members only, even though an individual Start can also retry a task. */
export function readyTreeStarts(tasks: readonly Task[], gate: (task: Task) => LaunchBlock | null) {
  const ready = tasks.filter((t) => t.readiness === "ready" && !t.adopted);
  const checked = ready.map((task) => ({ task, block: gate(task) }));
  return {
    tasks: checked.flatMap(({ task, block }) => block === null && canStart(task) ? [task] : []).toSorted((a, b) => a.id - b.id),
    readyCount: ready.length,
    firstBlock: checked.find(({ block }) => block !== null)?.block ?? null,
  };
}
