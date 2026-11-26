// Harness status as the sidebar status button and its details panel (lib/status.ts, components/StatusRow.tsx), a
// Settings connection row and a Config panel's Harness row (components/ConnectionState.tsx) read it: the generic
// reading and the shapes every harness's reader returns. A harness's own reader lives with it (src/harnesses/).
import type { ConnectionPhase, Harness, HarnessConnection, HarnessStatus, Machine } from "../api/types.ts";
import { connectedRows, connectionRows } from "./connectionRows.ts";
import { relativeTime } from "./format.ts";
import { machineSetupAlert, machineSetupState, machineStatusLabel } from "./machines.ts";
import type { StatusItem } from "./status.ts";

/** A status query as the control reads it; a data-less query returns to pending on every refetch, so only the first counts as checking. */
export function statusRead<T>(q: { data: T | undefined; isPending: boolean; errorUpdateCount: number; error: Error | null }): StatusRead<T> {
  return { data: q.data, checking: q.isPending && q.errorUpdateCount === 0, error: q.error?.message ?? null };
}

export type HarnessTone = "ok" | "need" | "bad" | "idle";

const phaseTone: Record<ConnectionPhase, HarnessTone> = {
  disconnected: "idle",
  needs_setup: "need",
  connecting: "idle",
  connected: "ok",
  degraded: "need",
  unavailable: "bad",
};

export function humanizeConnectionState(state: string): string {
  const label = state.replace(/[_-]+/g, " ").trim();
  return label === "" ? "" : label.charAt(0).toUpperCase() + label.slice(1);
}

export function connectionStatusChip(status: Pick<HarnessStatus, "phase" | "state">): { tone: HarnessTone; label: string } {
  return { tone: phaseTone[status.phase] ?? "idle", label: humanizeConnectionState(status.state || status.phase || "checking") };
}

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

/** A generic harness's summary, as its Settings row reads it: connected ok, configured but down bad, else idle. */
export function harnessSummary(read: StatusRead<HarnessStatus>, kind: Harness, name: string): HarnessSummary {
  if (read.checking) return { harness: kind, state: "checking", tone: "idle", label: `Checking ${name}` };
  const s = read.data;
  if (s === undefined) return { harness: kind, state: "offline", tone: "bad", label: `${name} offline` };
  const tone = phaseTone[s.phase] ?? (s.connected ? "ok" : s.configured ? "bad" : "idle");
  const state = s.phase === undefined ? s.state : humanizeConnectionState(s.state || s.phase).toLowerCase();
  return { harness: kind, state, tone, label: `${name} ${state}` };
}

/** A connection's detail lines and error line in the status panel. */
export interface StatusDetail {
  lines: string[];
  error: string | null;
}

/** A connection's live status as the status row and launch gate read it: its summary and its panel detail. */
export interface StatusView {
  summary: HarnessSummary;
  detail: StatusDetail;
}

/** A generic harness's status view: its summary, and why it is not connected (reason, else last error) as the detail. */
export function harnessStatusView(read: StatusRead<HarnessStatus>, kind: Harness, name: string): StatusView {
  const reason = read.data?.reason ?? read.data?.lastError ?? "";
  return { summary: harnessSummary(read, kind, name), detail: { lines: reason === "" ? [] : [reason], error: read.error } };
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
  machine: Pick<Machine, "kind" | "status" | "label" | "statusReason"> | undefined,
): ConnectionChip | "live" | null {
  if (connection === undefined || !connection.active || machine === undefined) return null;
  switch (machineSetupState(machine)) {
    case "offline":
      return { tone: "idle", label: "offline", title: `${machine.label} is offline`, fixable: false };
    case "negotiating":
    case "needs_update":
      return { tone: "idle", label: machineStatusLabel(machine.status), title: machineSetupAlert(machine) ?? "", fixable: false };
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
  machine: Pick<Machine, "id" | "kind" | "label" | "status" | "statusReason" | "lastSeenAt">;
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
 * the machine; otherwise one Harnesses section, as on a single machine. Negotiation counts as checking. An offline
 * machine or protocol mismatch is a red problem. `summary`: the live read on a machine that can; undefined: checking.
 */
export function connectionStatusItem(target: StatusTarget, name: string, summary: Pick<HarnessSummary, "tone" | "state" | "label"> | undefined, grouped: boolean): StatusItem {
  const { machine } = target;
  const base = { key: String(target.connectionId), group: grouped ? machine.label : "Harnesses", name };
  switch (machineSetupState(machine)) {
    case "offline":
      return { ...base, tone: "bad", state: "offline", label: `${machine.label} offline` };
    case "negotiating":
      return { ...base, tone: "idle", state: machineStatusLabel(machine.status), label: `Waiting for ${machine.label} to finish connecting` };
    case "needs_update":
      return { ...base, tone: "bad", state: machineStatusLabel(machine.status), label: `Stage update required for ${machine.label}'s connection` };
    case "ready":
      break;
  }
  if (summary === undefined) return { ...base, tone: "idle", state: "checking", label: `Checking ${name}` };
  return { ...base, tone: summary.tone, state: summary.state, label: grouped ? `${summary.label} on ${machine.label}` : summary.label };
}

/** Detail lines of a connection whose machine cannot answer. */
export function unreachableDetail(m: Omit<StatusTarget["machine"], "id">, now: number = Date.now()): string[] {
  switch (machineSetupState(m)) {
    case "offline":
      return [m.lastSeenAt === null ? "never seen" : `seen ${relativeTime(m.lastSeenAt, now)}`];
    case "negotiating":
    case "needs_update": {
      const reason = machineSetupAlert(m);
      return reason === null ? [] : [reason];
    }
    case "ready":
      return [];
  }
}
