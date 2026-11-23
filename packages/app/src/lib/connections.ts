// Per-harness status summaries for the sidebar status button and its details panel (lib/status.ts,
// components/StatusRow.tsx), the one-line detail of a Settings connection row, and the state chip of a Config
// panel's Harness row (components/ConnectionState.tsx).
import type { Harness, HarnessConnection, HarnessStatus, Machine, OpenCodeStatus, T3State, T3Status } from "../api/types.ts";
import { connectedRows, connectionRows } from "./connectionRows.ts";
import { relativeTime } from "./format.ts";
import { machineSetupState } from "./machines.ts";
import { T3_STATE_TONE, t3Identity } from "./onboarding.ts";
import { openCodeChip, serviceHostText, signInText } from "./opencode.ts";
import type { StatusItem } from "./status.ts";

/** A status query as the control reads it; a data-less query returns to pending on every refetch, so only the first counts as checking. */
export function statusRead<T>(q: { data: T | undefined; isPending: boolean; errorUpdateCount: number; error: Error | null }): StatusRead<T> {
  return { data: q.data, checking: q.isPending && q.errorUpdateCount === 0, error: q.error?.message ?? null };
}

/** A T3 row's detail: which T3 (label · version, else its URL) and when its token expires. */
export function t3RowDetail(s: T3Status, now: number = Date.now()): string {
  const expires = s.tokenExpiresAt;
  return [
    t3Identity(s) ?? s.t3Url,
    expires === null ? null : `token ${s.state === "expired" ? "expired" : "expires"} ${relativeTime(expires, now)}`,
  ]
    .filter((p) => p !== null && p !== "")
    .join(" · ");
}

/** An OpenCode row's detail: the service's host:port and how Stage signs in. */
export function openCodeRowDetail(s: OpenCodeStatus, now: number = Date.now()): string {
  return [s.endpoint === "" ? null : serviceHostText(s), signInText(s, now)].filter((p) => p !== null && p !== "").join(" · ");
}

export type HarnessTone = "ok" | "need" | "bad" | "idle";

/** A status query as the control reads it: `checking` before the first answer, `error` once a fetch failed. */
export interface StatusRead<T> {
  data: T | undefined;
  checking: boolean;
  error: string | null;
}

/** One harness section: its chip state and tone, and the label naming it for screen readers. */
export interface HarnessSummary {
  harness: Harness;
  state: string;
  tone: HarnessTone;
  label: string;
}

const T3_LABEL: Record<T3State, string> = {
  connected: "T3 connected",
  unpaired: "T3 not paired",
  expired: "T3 pairing expired",
  unreachable: "T3 unreachable",
  rejected: "T3 rejected the pairing",
};

export function t3Summary(read: StatusRead<T3Status>): HarnessSummary {
  if (read.checking) return { harness: "t3", state: "checking", tone: "idle", label: "Checking T3" };
  if (read.data === undefined) return { harness: "t3", state: "offline", tone: "bad", label: "T3 offline" };
  const state = read.data.state;
  return { harness: "t3", state, tone: T3_STATE_TONE[state], label: T3_LABEL[state] };
}

export function openCodeSummary(read: StatusRead<OpenCodeStatus>, now = Date.now()): HarnessSummary {
  if (read.checking) return { harness: "opencode", state: "checking", tone: "idle", label: "Checking OpenCode" };
  if (read.data === undefined) return { harness: "opencode", state: "offline", tone: "bad", label: "OpenCode offline" };
  const chip = openCodeChip(read.data, now);
  return { harness: "opencode", state: chip.label, tone: chip.tone, label: `OpenCode ${chip.label}` };
}

/** T3 section error line: the probe error while unreachable, else the last error, else the failed fetch. */
export function t3ErrorText(read: StatusRead<T3Status>): string | null {
  const data = read.data;
  return (data?.state === "unreachable" ? data.probeError : null) ?? data?.lastError ?? read.error;
}

/** OpenCode section error line: why it is unavailable or refused the sign-in, else the failed fetch. */
export function openCodeErrorText(read: StatusRead<OpenCodeStatus>): string | null {
  const data = read.data;
  if (data === undefined) return read.error;
  return data.state === "unavailable" || data.state === "rejected" ? (data.reason ?? null) : null;
}

/** A generic harness's summary, as its Settings row reads it: connected ok, configured but down bad, else idle. */
export function harnessSummary(read: StatusRead<HarnessStatus>, kind: Harness, name: string): HarnessSummary {
  if (read.checking) return { harness: kind, state: "checking", tone: "idle", label: `Checking ${name}` };
  const s = read.data;
  if (s === undefined) return { harness: kind, state: "offline", tone: "bad", label: `${name} offline` };
  const tone: HarnessTone = s.connected ? "ok" : s.configured ? "bad" : "idle";
  return { harness: kind, state: s.state, tone, label: `${name} ${s.state}` };
}

/** A Config panel Harness row's chip; `fixable`: Settings can fix it, so the row links there. */
export interface ConnectionChip {
  tone: HarnessTone;
  label: string;
  /** The chip's title: what it is and why (detail, error). */
  title: string;
  fixable: boolean;
}

/**
 * What a Config panel's Harness row shows before reading its connection: null for no chip (a replaced connection is
 * history and the row says so; one not loaded yet), a fixed chip when its machine cannot answer (as Settings records
 * it) or it is disconnected, else `live`: read its status on its machine (liveChip).
 */
export function connectionGate(
  connection: Pick<HarnessConnection, "active" | "configured"> | undefined,
  machine: Pick<Machine, "kind" | "status" | "label"> | undefined,
): ConnectionChip | "live" | null {
  if (connection === undefined || !connection.active || machine === undefined) return null;
  switch (machineSetupState(machine)) {
    case "offline":
      return { tone: "idle", label: "offline", title: `${machine.label} is offline`, fixable: false };
    case "needs_update":
      return { tone: "idle", label: "needs update", title: `${machine.label} needs an update`, fixable: false };
    case "ready":
      return connection.configured ? "live" : { tone: "idle", label: "disconnected", title: "Not connected. Connect it in Settings.", fixable: true };
  }
}

/** The chip from a connection's live status: its summary state and tone, titled with the Settings row's detail and the error; a problem links to Settings. */
export function liveChip(summary: Pick<HarnessSummary, "tone" | "state" | "label">, detail: string, error: string | null): ConnectionChip {
  const title = [summary.label, detail, error].filter((p) => p !== null && p !== "").join("\n");
  return { tone: summary.tone, label: summary.state, title, fixable: summary.tone === "need" || summary.tone === "bad" };
}

/** A set-up connection the status button reads, with the machine that runs it. */
export interface StatusTarget {
  connectionId: number;
  harness: Harness;
  machine: Pick<Machine, "id" | "kind" | "label" | "status" | "lastSeenAt" | "version">;
}

/** Every machine's set-up connections (its connected Settings rows; disconnected ones are not), in machine order. */
export function statusTargets(
  machines: readonly (StatusTarget["machine"] & Pick<Machine, "connections">)[],
  lookup: Parameters<typeof connectionRows>[1],
): StatusTarget[] {
  return machines.flatMap((machine) => connectedRows(connectionRows(machine, lookup)).map((r) => ({ connectionId: r.id, harness: r.harness, machine })));
}

/**
 * A connection's status item. With `grouped` (two or more machines) its section is its machine and its sentence names
 * the machine; otherwise one Harnesses section, as on a single machine. A machine that cannot answer (offline, needs an
 * update) is a red problem: its tasks cannot run. `summary`: the live read on a machine that can; undefined: checking.
 */
export function connectionStatusItem(target: StatusTarget, name: string, summary: Pick<HarnessSummary, "tone" | "state" | "label"> | undefined, grouped: boolean): StatusItem {
  const { machine } = target;
  const base = { key: String(target.connectionId), group: grouped ? machine.label : "Harnesses", name };
  switch (machineSetupState(machine)) {
    case "offline":
      return { ...base, tone: "bad", state: "offline", label: `${machine.label} offline` };
    case "needs_update":
      return { ...base, tone: "bad", state: "needs update", label: `${machine.label} needs an update` };
    case "ready":
      break;
  }
  if (summary === undefined) return { ...base, tone: "idle", state: "checking", label: `Checking ${name}` };
  return { ...base, tone: summary.tone, state: summary.state, label: grouped ? `${summary.label} on ${machine.label}` : summary.label };
}

/** Detail line of a connection whose machine cannot answer: when it was last seen, or the build it runs. */
export function unreachableDetail(m: Pick<Machine, "kind" | "status" | "lastSeenAt" | "version">, now: number = Date.now()): string[] {
  switch (machineSetupState(m)) {
    case "offline":
      return [m.lastSeenAt === null ? "never seen" : `seen ${relativeTime(m.lastSeenAt, now)}`];
    case "needs_update":
      return m.version === null ? [] : [`runs ${m.version}`];
    case "ready":
      return [];
  }
}
