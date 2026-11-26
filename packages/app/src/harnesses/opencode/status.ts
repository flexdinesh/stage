// OpenCode's status as the status button, its Settings row and a Config panel's Harness row read it.
import { relativeTime } from "../../lib/format.ts";
import type { HarnessSummary, StatusRead, StatusView } from "../../lib/connections.ts";
import { OPENCODE_KIND } from "./api.ts";
import { openCodeChip, serviceHostText, signInText } from "./opencode.ts";
import type { OpenCodeStatus } from "./types.ts";

/** An OpenCode row's detail: the service's host:port and how Stage signs in. */
export function openCodeRowDetail(s: OpenCodeStatus, now: number = Date.now()): string {
  return [s.endpoint === "" ? null : serviceHostText(s), signInText(s, now)].filter((p) => p !== null && p !== "").join(" · ");
}

export function openCodeSummary(read: StatusRead<OpenCodeStatus>, now = Date.now()): HarnessSummary {
  if (read.checking) return { harness: OPENCODE_KIND, state: "checking", tone: "idle", label: "Checking OpenCode" };
  if (read.data === undefined) return { harness: OPENCODE_KIND, state: "offline", tone: "bad", label: "OpenCode offline" };
  const chip = openCodeChip(read.data, now);
  return { harness: OPENCODE_KIND, state: chip.label, tone: chip.tone, label: `OpenCode ${chip.label}` };
}

/** OpenCode section error line: why it is unavailable or refused the sign-in, else the failed fetch. */
export function openCodeErrorText(read: StatusRead<OpenCodeStatus>): string | null {
  const data = read.data;
  if (data === undefined) return read.error;
  return data.state === "unavailable" || data.state === "rejected" ? (data.reason ?? null) : null;
}

/** OpenCode's status view: the service's host and sign-in, then when it was checked. */
export function openCodeStatusView(read: StatusRead<OpenCodeStatus>, now: number = Date.now()): StatusView {
  const d = read.data;
  const lines =
    d === undefined
      ? []
      : [[d.endpoint === "" ? null : serviceHostText(d), signInText(d, now)].filter((p) => p !== null).join(" · "), `checked ${relativeTime(d.checkedAt ?? null, now)}`];
  return { summary: openCodeSummary(read, now), detail: { lines, error: openCodeErrorText(read) } };
}
