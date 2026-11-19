// T3 connection status and recovery actions. Planning remains available offline.
import type { AutoPairReason, T3State, T3Status } from "../api/types.ts";

/** True once, when a refetch sees the first full sync land: work, projects and catalog refresh then. */
export function firstSyncLanded(prev: T3Status | undefined, next: T3Status): boolean {
  return prev !== undefined && prev.lastFullSyncAt === null && next.lastFullSyncAt !== null;
}

/** Tone of a T3 state wherever it is shown (status control, chips, alerts). */
export const T3_STATE_TONE: Record<T3State, "ok" | "need" | "bad"> = {
  connected: "ok",
  unpaired: "need",
  expired: "need",
  unreachable: "bad",
  rejected: "bad",
};

export type SetupStepKey = "t3" | "pairing" | "first sync";
export type SetupStepStatus = "idle" | "active" | "ok" | "need" | "bad";

export interface SetupStep {
  key: SetupStepKey;
  status: SetupStepStatus;
  note: string;
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

/** `controlbox · v0.1.9`; "reachable" when T3 did not identify itself. */
export function t3Identity(s: T3Status): string | null {
  if (s.t3 === null) return null;
  const version = s.t3.serverVersion === "" ? "" : s.t3.serverVersion.startsWith("v") ? s.t3.serverVersion : `v${s.t3.serverVersion}`;
  return [s.t3.label, version].filter((p) => p !== "").join(" · ");
}

function t3Step(s: T3Status, checking: boolean): SetupStep {
  if (checking || (s.checkedAt === null && !s.reachable)) return { key: "t3", status: "active", note: "checking" };
  if (!s.reachable) return { key: "t3", status: "bad", note: `not found at ${s.t3Url}` };
  return { key: "t3", status: "ok", note: t3Identity(s) ?? "reachable" };
}

const pairing = (status: SetupStepStatus, note: string): SetupStep => ({ key: "pairing", status, note });

function pairingStep(s: T3Status): SetupStep {
  switch (s.state) {
    case "connected":
      return pairing("ok", "paired");
    case "unpaired":
      return pairing("idle", "not paired");
    case "expired":
      return pairing("need", "expired");
    case "rejected":
      return pairing("bad", "rejected");
    case "unreachable":
      // The token cannot be checked while T3 is away.
      if (s.connected) return pairing("ok", "paired");
      return pairing("idle", s.tokenExpiresAt === null ? "not paired" : "waiting");
  }
}

function syncStep(s: T3Status): SetupStep {
  if (s.lastFullSyncAt !== null) return { key: "first sync", status: "ok", note: "done" };
  if (s.sync.running) {
    const counted = s.sync.projects > 0 || s.sync.threads > 0;
    return {
      key: "first sync",
      status: "active",
      note: counted ? `${plural(s.sync.projects, "project")} · ${plural(s.sync.threads, "thread")}` : "starting",
    };
  }
  return { key: "first sync", status: "idle", note: "waiting" };
}

/** The three setup rows; `checking` marks a retry in flight. */
export function setupSteps(s: T3Status, checking = false): SetupStep[] {
  return [t3Step(s, checking && !s.reachable), pairingStep(s), syncStep(s)];
}

/** One primary action per state; null while nothing needs pressing (syncing, or done). */
export type SetupAction = "retry" | "autoPair" | "code" | "sync";

export function primaryAction(s: T3Status): SetupAction | null {
  switch (s.state) {
    case "unreachable":
      return "retry";
    case "unpaired":
    case "expired":
    case "rejected":
      return s.autoPair.available ? "autoPair" : "code";
    case "connected":
      return s.lastFullSyncAt === null && !s.sync.running ? "sync" : null;
  }
}

/** Settings Disconnect: hidden without a stored token; locked while T3_ADMIN_TOKEN would mint a new one at once. */
export type DisconnectAvailability = "hidden" | "locked" | "ready";

export function disconnectAvailability(s: T3Status): DisconnectAvailability {
  if (!s.connected && s.tokenExpiresAt === null) return "hidden";
  return s.adminTokenConfigured ? "locked" : "ready";
}

/** Why pairing needs a code, when auto-pair is unavailable. */
export function codeReason(reason: AutoPairReason | null): string {
  switch (reason) {
    case "not_local":
      return "T3 runs on another machine. Create a pairing code there and paste it here.";
    case "cli_not_found":
      return "The t3 CLI was not found on this machine. Create a pairing code where T3 runs and paste it here.";
    case "fake":
      return "The fake T3 client pairs with any code.";
    case null:
      return "Create a pairing code where T3 runs and paste it here.";
  }
}

export interface DashboardAlert {
  tone: "need" | "bad";
  message: string;
}

/** Connection trouble is an Alert; planning never depends on pairing or sync. */
export function dashboardAlert(s: T3Status | undefined): DashboardAlert | null {
  if (s === undefined) return null;
  switch (s.state) {
    case "connected":
      return null;
    case "unreachable":
      return { tone: "bad", message: `No T3 server answered at ${s.t3Url}.` };
    case "unpaired":
      return { tone: "need", message: "stage is not paired with T3. Pair to keep threads in sync." };
    case "expired":
      return { tone: "need", message: "The T3 pairing expired. Pair again to keep threads in sync." };
    case "rejected":
      return { tone: "bad", message: "T3 rejected stage's pairing. Pair again to keep threads in sync." };
  }
}
