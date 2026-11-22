// Pure logic of the host-managed connection UI (components/Host.tsx).
import type { HostPhase, HostServerEntry, HostServerTest, HostState, HostTarget, HostWorker, HostWorkerStatus } from "../platform/index.ts";

export type StepKey = "service" | "server" | "api";
export type StepStatus = "idle" | "active" | "ok" | "bad";

export const STEPS: readonly StepKey[] = ["service", "server", "api"];

/** The steps the connection screen lists: a remote has no service (the host never manages it). */
export function visibleSteps(state: HostState): readonly StepKey[] {
  return state.target.kind === "remote" ? ["server", "api"] : STEPS;
}

/** The step a failing phase points at. */
const FAILED_STEP: Partial<Record<HostPhase, StepKey>> = {
  unsupported: "service",
  misconfigured: "service",
  failed: "server",
  stopped: "server",
  conflict: "server",
  reconnecting: "server",
  unreachable: "server",
  incompatible: "api",
};

export function stepStatus(state: HostState, key: StepKey): StepStatus {
  if (state.phase === "ready") return "ok";
  const at = state.phase === "starting" ? state.step : (FAILED_STEP[state.phase] ?? null);
  const current = at === null ? -1 : STEPS.indexOf(at);
  if (current < 0) return "idle";
  const order = STEPS.indexOf(key);
  if (order < current) return "ok";
  if (order > current) return "idle";
  return state.phase === "starting" ? "active" : "bad";
}

export function stepNote(state: HostState, key: StepKey, status: StepStatus): string {
  if (status === "idle") return "waiting";
  if (status === "active") return key === "api" ? "checking" : "starting";
  if (status === "bad") {
    if (key === "service") return state.phase === "misconfigured" ? "misconfigured" : "unavailable";
    if (key === "api") return "incompatible";
    return state.phase === "unreachable" ? "unreachable" : "not running";
  }
  if (key === "server" && state.server !== null) return state.server.version;
  if (key === "api" && state.server !== null) return `v${state.server.apiVersion}`;
  return "ok";
}

export type HostAction = "start" | "restart" | "retry";

/** The primary recovery for a phase; null when there is nothing to press. A remote only offers Retry: the host never starts it. */
export function primaryAction(phase: HostPhase, kind: HostTarget["kind"] = "local"): HostAction | null {
  if (kind === "remote") return phase === "starting" || phase === "ready" ? null : "retry";
  switch (phase) {
    case "stopped":
    case "failed":
      return "start";
    case "incompatible":
      return "restart";
    case "unsupported":
    case "misconfigured":
    case "conflict":
    case "reconnecting":
    case "unreachable":
      return "retry";
    case "starting":
    case "ready":
    case "worker": // the worker screen offers Switch and Leave instead
      return null;
  }
}

export const ACTION_LABEL: Record<HostAction, string> = {
  start: "Start Stage server",
  restart: "Restart Stage server",
  retry: "Retry",
};

/**
 * What the gate shows: the app once the active server has been ready (later
 * trouble keeps it mounted), a blank ground for the first moments of a start
 * (no flash on a fast start), else the connection screen. A server on
 * another API version blocks even after the app ran. A switch starts over:
 * the new server was never ready, so an unreachable one shows the screen.
 */
export function gateView(state: HostState | null, startingLate: boolean): "app" | "blank" | "screen" {
  if (state === null) return "app";
  if (state.phase === "incompatible") return "screen";
  if (state.phase === "ready" || state.serverEpoch > 0) return "app";
  if (state.phase === "starting" && !startingLate) return "blank";
  return "screen";
}

/**
 * A server switch that leaves the page holding the old server's data: the target changed after the app ran on
 * it. The first state from the host (replacing the placeholder) is not one, so a launch deep link survives.
 */
export function isServerSwitch(prev: HostState, next: HostState): boolean {
  return prev.target.id !== next.target.id && prev.serverEpoch > 0;
}

export type ServerTone = "ok" | "run" | "bad" | "idle";

/** A saved server's marker and note: only the active one has a known state; the others read idle, no note. */
export function serverStatus(entry: HostServerEntry, state: HostState | null): { tone: ServerTone; note: string } {
  if (state === null || !entry.active || state.target.id !== entry.id) return { tone: "idle", note: "" };
  switch (state.phase) {
    case "ready":
      return { tone: "ok", note: "connected" };
    case "starting":
      return { tone: "run", note: "connecting" };
    case "reconnecting":
      return { tone: "run", note: "reconnecting" };
    case "unreachable":
      return { tone: "bad", note: "unreachable" };
    case "incompatible":
      return { tone: "bad", note: "incompatible" };
    case "stopped":
      return { tone: "bad", note: "not running" };
    case "failed":
    case "unsupported":
    case "misconfigured":
    case "conflict":
      return { tone: "bad", note: "failed" };
    case "worker":
      return { tone: "idle", note: "worker" };
  }
}

/** The mono line under a server's name: this machine (and its socket while active), or the remote's URL. */
export function serverWhere(entry: HostServerEntry, state: HostState | null): string {
  if (entry.kind === "remote") return entry.url;
  if (state?.worker != null) return `this machine · worker of ${state.worker.serverName}`;
  const socket = state !== null && state.target.id === entry.id ? (state.server?.socket ?? "") : "";
  return socket === "" ? "this machine" : `this machine · ${socket}`;
}

export type ServerTestKey = "reachable" | "api" | "identity";

/** The Test connection step rows: idle before a test, the reachable step spinning while it runs, then the result. */
export function serverTestSteps(test: HostServerTest | null, testing: boolean): { key: ServerTestKey; status: StepStatus; note: string }[] {
  if (testing) {
    return [
      { key: "reachable", status: "active", note: "checking" },
      { key: "api", status: "idle", note: "waiting" },
      { key: "identity", status: "idle", note: "waiting" },
    ];
  }
  if (test === null) {
    return [
      { key: "reachable", status: "idle", note: "not tested" },
      { key: "api", status: "idle", note: "waiting" },
      { key: "identity", status: "idle", note: "waiting" },
    ];
  }
  if (!test.reachable) {
    return [
      { key: "reachable", status: "bad", note: "no answer" },
      { key: "api", status: "idle", note: "-" },
      { key: "identity", status: "idle", note: "-" },
    ];
  }
  const api: { status: StepStatus; note: string } =
    test.apiVersion === 0
      ? { status: "bad", note: test.command === "" ? "not a Stage server" : "refused" }
      : { status: test.error === "" ? "ok" : "bad", note: `v${test.apiVersion}` };
  const identity: { status: StepStatus; note: string } =
    test.identity === "" ? { status: "idle", note: "-" } : { status: "ok", note: test.version === "" ? test.identity : `${test.identity} · ${test.version}` };
  return [
    { key: "reachable", status: "ok", note: "answers" },
    { key: "api", ...api },
    { key: "identity", ...identity },
  ];
}

// ---- Worker (this machine as a worker of a remote server) ----

/** The worker of the active remote server; null when this machine serves another server, or none. */
export function activeWorker(state: HostState | null): HostWorker | null {
  if (state === null || state.worker === null || state.target.kind !== "remote") return null;
  return state.worker.serverId === state.target.id ? state.worker : null;
}

/** The marker tone of a worker status: online ok, starting run, offline idle (as Settings > Machines), trouble bad. */
export function workerTone(status: HostWorkerStatus): ServerTone {
  switch (status) {
    case "online":
      return "ok";
    case "starting":
      return "run";
    case "offline":
      return "idle";
    case "needs_update":
    case "removed":
    case "failed":
      return "bad";
  }
}

const WORKER_NOTE: Record<HostWorkerStatus, string> = {
  starting: "starting",
  online: "online",
  offline: "offline",
  needs_update: "needs update",
  removed: "removed",
  failed: "failed",
};

/** The mono status line of a worker: `online · machine 4`. */
export function workerLine(w: HostWorker): string {
  return `${WORKER_NOTE[w.status]} · machine ${w.machineId}`;
}

/** The banner tone of the active server's worker: none while online or starting, plain while offline, bad on trouble. */
export function workerBanner(w: HostWorker | null): "plain" | "bad" | null {
  if (w === null) return null;
  switch (w.status) {
    case "online":
    case "starting":
      return null;
    case "offline":
      return "plain";
    case "needs_update":
    case "removed":
    case "failed":
      return "bad";
  }
}

/** The banner headline of a worker in trouble. */
export function workerHeadline(w: HostWorker): string {
  switch (w.status) {
    case "removed":
      return `${w.serverName} removed this machine`;
    case "needs_update":
      return "This machine's worker needs an update";
    case "failed":
      return "This machine's worker failed";
    default:
      return "This machine's worker is offline";
  }
}

/** Whether Restart worker can help: not while online, starting or removed (that needs a new join). */
export function canRestartWorker(w: HostWorker): boolean {
  return w.status === "offline" || w.status === "failed" || w.status === "needs_update";
}

/**
 * The inline confirm before leaving: what the main server does, and how this machine gets its server back (at once
 * from this machine's own screen, else when switching to it).
 */
export function leavePrompt(w: HostWorker, onThisMachine = false): string[] {
  const back = onThisMachine ? "This machine's own Stage server starts again." : "This machine's own Stage server runs again when you switch to This machine.";
  return [`Leave ${w.serverName}?`, `${w.serverName} removes this machine and retires its harness connections. ${back}`];
}

/** Set up harnesses opens the machine's page on the active server: only for its worker, and not once removed. */
export function canSetUpWorker(w: HostWorker, ofActive: boolean): boolean {
  return ofActive && w.status !== "removed";
}
