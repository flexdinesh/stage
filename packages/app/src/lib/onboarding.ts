// T3 connection status and recovery actions. Planning remains available offline.
import type { AutoPairReason, T3SettingsRequest, T3State, T3Status } from "../api/types.ts";

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

/** T3 is set up (paired): a stored token or a saved admin token. Unpaired, it has no connection row, status card or Disconnect. */
export function t3Configured(s: Pick<T3Status, "connected" | "tokenExpiresAt" | "adminTokenConfigured">): boolean {
  return s.connected || s.tokenExpiresAt !== null || s.adminTokenConfigured;
}

/** Settings Disconnect: shown whenever T3 is set up. */
export type DisconnectAvailability = "hidden" | "ready";

export function disconnectAvailability(s: Pick<T3Status, "connected" | "tokenExpiresAt" | "adminTokenConfigured">): DisconnectAvailability {
  return t3Configured(s) ? "ready" : "hidden";
}

/** The Disconnect confirm's copy: what Stage forgets, what stays, and until when the token still works in T3. */
export function t3DisconnectPrompt(s: Pick<T3Status, "t3" | "connected" | "tokenExpiresAt">, date: (iso: string) => string): string {
  const label = s.t3 === null || s.t3.label === "" ? null : s.t3.label;
  // `connected` is a valid token T3 has not rejected: only then does it outlive the disconnect.
  const expires = s.connected ? s.tokenExpiresAt : null;
  return [
    label === null ? "Disconnect T3?" : `Disconnect from ${label}?`,
    "Stage forgets its token, URL, browser URL and admin token and stops syncing. Tasks and bindings stay; pair the same T3 again to resume.",
    "It stays listed as disconnected until you forget it.",
    expires === null ? null : `The token stays valid in T3 until ${date(expires)}.`,
  ]
    .filter((p) => p !== null)
    .join(" ");
}

/** The Browser URL as saved: publicUrl falls back to the T3 URL when none is. */
export const savedBrowserUrl = (s: Pick<T3Status, "publicUrl" | "t3Url">): string => (s.publicUrl === s.t3Url ? "" : s.publicUrl);

export type T3SettingsDraft = { url: string; browserUrl: string; adminToken: string };

/**
 * T3 settings save body, only what changed (trimmed): the T3 URL (empty is ignored: Reset restores the default), the
 * Browser URL (empty clears it), and an entered admin token.
 */
export function t3SettingsChanges(s: Pick<T3Status, "publicUrl" | "t3Url">, d: T3SettingsDraft): T3SettingsRequest {
  const body: T3SettingsRequest = {};
  const url = d.url.trim();
  const browser = d.browserUrl.trim();
  if (url !== "" && url !== s.t3Url) body.url = url;
  if (browser !== savedBrowserUrl(s)) body.publicUrl = browser === "" ? null : browser;
  if (d.adminToken !== "") body.adminToken = d.adminToken;
  return body;
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
