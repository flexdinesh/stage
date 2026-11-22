// Per-harness status summaries for the sidebar status button and its details panel (lib/status.ts,
// components/StatusRow.tsx), and the one-line detail of a Settings connection row.
import type { Harness, OpenCodeStatus, T3State, T3Status } from "../api/types.ts";
import { relativeTime } from "./format.ts";
import { T3_STATE_TONE, t3Identity } from "./onboarding.ts";
import { openCodeChip, serviceHostText, signInText } from "./opencode.ts";

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
