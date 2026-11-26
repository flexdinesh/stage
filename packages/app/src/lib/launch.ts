import type { Capabilities, HarnessConnection, Machine, ProjectBinding, Task } from "../api/types.ts";
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

export type SessionOperation = "launch" | "stop" | "adopt";

const operationTarget = { launch: "start this task", stop: "stop this task", adopt: "track sessions" };
const operationName = { launch: "starting tasks", stop: "stopping tasks", adopt: "tracking sessions" };

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

/** What `launchBlock` reads about a task's connection; undefined parts are not loaded yet and block nothing. */
export interface LaunchContext {
  connection: Pick<HarnessConnection, "active" | "configured"> | undefined;
  machine: Pick<Machine, "kind" | "status" | "label" | "statusReason"> | undefined;
  /** Its live status on its machine; undefined until read (checking blocks nothing). */
  live: Pick<HarnessSummary, "tone" | "label"> | undefined;
  /** The task's project bindings; undefined until projects load. */
  bindings: readonly Pick<ProjectBinding, "connectionId" | "status">[] | undefined;
  /** The connection's display name ("T3", or "T3 · desk" with two or more machines). */
  name: string;
  /** "on desk" with two or more machines, else "". */
  where: string;
  /** Where Settings fixes a connection. */
  settings: string;
}

/**
 * Why Start cannot launch the task on its connection now, or null when it can (as far as Stage knows; the server
 * checks again). In order: disconnected, its machine unavailable, the harness not answering (unreachable,
 * unpaired, expired, rejected), the project without an available workspace there. A replaced connection is not a
 * block here: it never launches again (isRetired).
 */
export function connectionBlock(c: Omit<LaunchContext, "bindings">, op: SessionOperation): LaunchBlock | null {
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

export function launchBlock(t: Pick<Task, "connectionId" | "projectId" | "projectTitle">, c: LaunchContext): LaunchBlock | null {
  const block = connectionBlock(c, "launch");
  if (block !== null) return block;
  const { connection, bindings, name } = c;
  if (connection === undefined || !connection.active) return null;
  if (bindings !== undefined && !bindings.some((b) => b.connectionId === t.connectionId && b.status === "available")) {
    return {
      kind: "unbound",
      reason: `${t.projectTitle} has no workspace on ${name}. Bind one to start this task.`,
      fix: { label: "Bind workspace", to: `/projects/${t.projectId}` },
    };
  }
  return null;
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
