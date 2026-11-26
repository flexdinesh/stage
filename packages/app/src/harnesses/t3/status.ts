// T3's status as the status button, its Settings row and a Config panel's Harness row read it.
import { relativeTime } from "../../lib/format.ts";
import type { HarnessSummary, StatusRead, StatusView } from "../../lib/connections.ts";
import { T3_KIND } from "./api.ts";
import { T3_STATE_TONE, t3Identity } from "./onboarding.ts";
import type { T3State, T3Status } from "./types.ts";

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

const T3_LABEL: Record<T3State, string> = {
  connected: "T3 connected",
  unpaired: "T3 not paired",
  expired: "T3 pairing expired",
  unreachable: "T3 unreachable",
  rejected: "T3 rejected the pairing",
};

export function t3Summary(read: StatusRead<T3Status>): HarnessSummary {
  if (read.checking) return { harness: T3_KIND, state: "checking", tone: "idle", label: "Checking T3" };
  if (read.data === undefined) return { harness: T3_KIND, state: "offline", tone: "bad", label: "T3 offline" };
  const state = read.data.state;
  return { harness: T3_KIND, state, tone: T3_STATE_TONE[state], label: T3_LABEL[state] };
}

/** T3 section error line: the probe error while unreachable, else the last error, else the failed fetch. */
export function t3ErrorText(read: StatusRead<T3Status>): string | null {
  const data = read.data;
  return (data?.state === "unreachable" ? data.probeError : null) ?? data?.lastError ?? read.error;
}

/** T3's status view: which T3 and its URL, then the last full sync and when the token expires. */
export function t3StatusView(read: StatusRead<T3Status>, now: number = Date.now()): StatusView {
  const d = read.data;
  const expires = d?.tokenExpiresAt ?? null;
  const lines = [
    [d === undefined ? null : t3Identity(d), d?.t3Url ?? null].filter((p) => p !== null && p !== "").join(" · "),
    [
      d?.lastFullSyncAt == null ? null : `synced ${relativeTime(d.lastFullSyncAt, now)}`,
      expires === null ? null : `token ${d?.state === "expired" ? "expired" : "expires"} ${relativeTime(expires, now)}`,
    ]
      .filter((p) => p !== null)
      .join(" · "),
  ].filter((l) => l !== "");
  return { summary: t3Summary(read), detail: { lines, error: t3ErrorText(read) } };
}
