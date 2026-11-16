// Pure logic of the host-managed connection UI (components/Host.tsx).
import type { HostPhase, HostState } from "../platform/index.ts";

export type StepKey = "service" | "server" | "api";
export type StepStatus = "idle" | "active" | "ok" | "bad";

export const STEPS: readonly StepKey[] = ["service", "server", "api"];

/** The step a failing phase points at. */
const FAILED_STEP: Partial<Record<HostPhase, StepKey>> = {
  unsupported: "service",
  misconfigured: "service",
  failed: "server",
  stopped: "server",
  conflict: "server",
  reconnecting: "server",
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
    return key === "api" ? "incompatible" : "not running";
  }
  if (key === "server" && state.server !== null) return state.server.version;
  if (key === "api" && state.server !== null) return `v${state.server.apiVersion}`;
  return "ok";
}

export type HostAction = "start" | "restart" | "retry";

/** The primary recovery for a phase; null when there is nothing to press. */
export function primaryAction(phase: HostPhase): HostAction | null {
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
      return "retry";
    case "starting":
    case "ready":
      return null;
  }
}

export const ACTION_LABEL: Record<HostAction, string> = {
  start: "Start Stage server",
  restart: "Restart Stage server",
  retry: "Retry",
};

/**
 * What the gate shows: the app once the connection has been ready (later
 * trouble keeps it mounted), a blank ground for the first moments of a start
 * (no flash on a fast start), else the connection screen. A server on
 * another API version blocks even after the app ran.
 */
export function gateView(state: HostState | null, startingLate: boolean): "app" | "blank" | "screen" {
  if (state === null) return "app";
  if (state.phase === "incompatible") return "screen";
  if (state.phase === "ready" || state.epoch > 0) return "app";
  if (state.phase === "starting" && !startingLate) return "blank";
  return "screen";
}
