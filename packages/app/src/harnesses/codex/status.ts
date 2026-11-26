// Codex's status as the status button, its Settings row and a Config panel's Harness row read it.
import { relativeTime } from "../../lib/format.ts";
import { harnessSummary, type HarnessSummary, type HarnessTone, type StatusRead, type StatusView } from "../../lib/connections.ts";
import { CODEX_KIND, codexAccountShort, codexState } from "./codex.ts";
import type { CodexState, CodexStatus } from "./types.ts";

/** A Codex row's detail: the CLI version and who it is signed in as. */
export function codexRowDetail(s: CodexStatus): string {
  const d = s.details;
  if (d === null) return "";
  return [d.cliVersion === "" ? null : `codex-cli ${d.cliVersion}`, codexAccountShort(d.account)].filter((p) => p !== null).join(" · ");
}

const CODEX_CHIP: Record<CodexState, { state: string; tone: HarnessTone; label: string }> = {
  connected: { state: "connected", tone: "ok", label: "Codex connected" },
  signed_out: { state: "signed out", tone: "need", label: "Codex signed out" },
  unreachable: { state: "unreachable", tone: "bad", label: "Codex unreachable" },
  not_installed: { state: "not installed", tone: "bad", label: "Codex not installed" },
  not_connected: { state: "not connected", tone: "idle", label: "Codex not connected" },
};

/** Codex's summary: signed out asks the user (need), unreachable or not installed is a fault; an unknown state reads generically. */
export function codexSummary(read: StatusRead<CodexStatus>): HarnessSummary {
  if (read.checking) return { harness: CODEX_KIND, state: "checking", tone: "idle", label: "Checking Codex" };
  const s = read.data;
  if (s === undefined) return { harness: CODEX_KIND, state: "offline", tone: "bad", label: "Codex offline" };
  const state = codexState(s.state);
  return state === null ? harnessSummary(read, CODEX_KIND, "Codex") : { harness: CODEX_KIND, ...CODEX_CHIP[state] };
}

/** Codex section error line: why it is not connected (reason, else last error), else the failed fetch. */
export function codexErrorText(read: StatusRead<CodexStatus>): string | null {
  const s = read.data;
  if (s === undefined) return read.error;
  if (s.connected) return null;
  const reason = s.reason ?? s.lastError ?? "";
  return reason === "" ? null : reason;
}

/** Codex's status view: the CLI version and account, then when it was checked. */
export function codexStatusView(read: StatusRead<CodexStatus>, now: number = Date.now()): StatusView {
  const d = read.data;
  const checkedAt = d?.details?.checkedAt ?? null;
  const lines = d === undefined ? [] : [codexRowDetail(d), checkedAt === null ? "" : `checked ${relativeTime(checkedAt, now)}`].filter((l) => l !== "");
  return { summary: codexSummary(read), detail: { lines, error: codexErrorText(read) } };
}
