// Claude Code's connection (kind `claude`): the state chip, the Settings row's detail line, the status panel view and
// the fix a problem state asks for. Its status is the generic GET /harnesses/claude/status; its `details` are read
// with claudeDetails (docs/api.md). Components: components/ClaudeSetup.tsx.
import type { HarnessStatus } from "../api/types.ts";
import type { HarnessSummary, HarnessTone, StatusRead, StatusView } from "./connections.ts";
import { relativeTime } from "./format.ts";

export const CLAUDE = "claude";

/** Disconnect's inline confirm (Settings row and card). */
export const CLAUDE_DISCONNECT_PROMPT =
  "Disconnect Claude Code? Stage stops starting and following Claude sessions here; running sessions keep going in Claude. Tasks and history stay, and connecting again resumes them.";

/** GET /api/harnesses/claude/status `details`; empty strings until the first CLI check ran. */
export interface ClaudeDetails {
  /** The resolved claude executable. */
  binary: string;
  /** As `claude --version` prints it ("2.1.332 (Claude Code)"). */
  version: string;
  /** Claude's config folder: the connection's identity. */
  configDirectory: string;
  loggedIn: boolean;
  /** Background launches run in their own systemd user scope, so sessions outlive a Stage restart. */
  scope: boolean;
  /** How Stage hears about the sessions it launches; null before the connection is set up. */
  events: ClaudeEvents | null;
}

/** `details.events`: Claude hooks wake Stage (push, polling still reconciles), or Stage only polls and says why. */
export interface ClaudeEvents {
  mode: "push" | "polling";
  /** Why Stage only polls ("" when pushing). */
  reason: string;
  /** The last hook Stage accepted (ISO); null for none since Stage started. */
  lastEventAt: string | null;
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === "string" ? v : "");

function eventsOf(v: unknown): ClaudeEvents | null {
  if (!isRecord(v) || (v.mode !== "push" && v.mode !== "polling")) return null;
  const at = str(v.lastEventAt);
  return { mode: v.mode, reason: str(v.reason), lastEventAt: at === "" ? null : at };
}

/** The status's Claude details; null when the server sent none. */
export function claudeDetails(s: Pick<HarnessStatus, "details">): ClaudeDetails | null {
  const d = s.details;
  if (!isRecord(d)) return null;
  return {
    binary: str(d.binary),
    version: str(d.version),
    configDirectory: str(d.configDirectory),
    loggedIn: d.loggedIn === true,
    scope: d.scope === true,
    events: eventsOf(d.events),
  };
}

/**
 * How updates arrive, for the card's Updates line: "instant · last hook 2m ago" (or "no hook yet") while hooks push,
 * "every few seconds" with the reason while Stage only polls. Polling is not a problem: neutral, never a tone.
 */
export function updatesText(events: ClaudeEvents, now: number = Date.now()): { text: string; reason: string | null } {
  if (events.mode === "polling") return { text: "every few seconds", reason: events.reason === "" ? null : events.reason };
  return { text: `instant · ${events.lastEventAt === null ? "no hook yet" : `last hook ${relativeTime(events.lastEventAt, now)}`}`, reason: null };
}

/** The card's Approvals fact: while hooks push, a session's approvals and questions are answered on its task too. */
export function approvalsText(events: ClaudeEvents): string {
  return events.mode === "push" ? "on the task in Stage, or in Claude Code" : "in Claude Code";
}

/** connected | disconnected | not_found | logged_out | error. */
type ClaudeState = "connected" | "disconnected" | "not_found" | "logged_out" | "error";

const LABEL: Record<ClaudeState, string> = {
  connected: "connected",
  disconnected: "not connected",
  not_found: "not found",
  logged_out: "logged out",
  error: "not answering",
};

const isState = (s: string): s is ClaudeState => Object.hasOwn(LABEL, s);

type Status = Pick<HarnessStatus, "state" | "connected" | "configured" | "reason" | "details">;

/** The state chip: connected ok; a problem bad once set up, idle before (nothing depends on it yet). */
export function claudeChip(s: Pick<HarnessStatus, "state" | "connected" | "configured">): { tone: HarnessTone; label: string } {
  const label = isState(s.state) ? LABEL[s.state] : s.state;
  if (s.connected) return { tone: "ok", label };
  if (s.state === "disconnected") return { tone: "idle", label };
  return { tone: s.configured ? "bad" : "idle", label };
}

/** "~/.claude" for Claude's default config folder (`<home>/.claude`); any other folder in full. */
export function configText(configDirectory: string): string {
  const m = /^\/(?:home|Users)\/[^/]+\/(\.claude)$/.exec(configDirectory) ?? /^\/root\/(\.claude)$/.exec(configDirectory);
  return m === null ? configDirectory : `~/${m[1] ?? ".claude"}`;
}

/** "2.1.332" from "2.1.332 (Claude Code)". */
export function versionText(version: string): string {
  return version.replace(/\s*\(Claude Code\)\s*$/, "");
}

const reasonOf = (s: Pick<HarnessStatus, "connected" | "reason">): string | null =>
  !s.connected && s.reason !== undefined && s.reason !== "" ? s.reason : null;

/** "~/.claude · 2.1.332": which Claude and its version, as far as the checks got. */
export function claudeIdentity(s: Pick<HarnessStatus, "details">): string {
  const d = claudeDetails(s);
  if (d === null) return "";
  return [d.configDirectory === "" ? null : configText(d.configDirectory), d.version === "" ? null : versionText(d.version)].filter((p) => p !== null).join(" · ");
}

/** A Settings row's detail: why it is not connected, else which Claude. */
export function claudeRowDetail(s: Status): string {
  return reasonOf(s) ?? claudeIdentity(s);
}

/** The claude command Stage runs and where it came from: the saved Claude command, else Stage's own lookup. */
export function commandText(s: Pick<HarnessStatus, "fields" | "details">): { command: string; source: "saved" | "found on PATH" | "default" } {
  const field = s.fields.find((f) => f.id === "binary");
  const saved = field !== undefined && field.source === "saved" && field.value !== "";
  const resolved = claudeDetails(s)?.binary ?? "";
  if (saved) return { command: resolved === "" ? field.value : resolved, source: "saved" };
  return resolved === "" ? { command: "claude", source: "default" } : { command: resolved, source: "found on PATH" };
}

/** What fixes a problem state in a terminal; null when Connect or the Claude command field is the fix. */
export function claudeFix(s: Pick<HarnessStatus, "state">): string | null {
  return s.state === "logged_out" ? "claude auth login" : null;
}

/** The status button's summary for Claude Code. */
export function claudeSummary(read: StatusRead<HarnessStatus>, name: string): HarnessSummary {
  if (read.checking) return { harness: CLAUDE, state: "checking", tone: "idle", label: `Checking ${name}` };
  const s = read.data;
  if (s === undefined) return { harness: CLAUDE, state: "offline", tone: "bad", label: `${name} offline` };
  const chip = claudeChip(s);
  return { harness: CLAUDE, state: chip.label, tone: chip.tone, label: `${name} ${chip.label}` };
}

/** The status panel: which Claude and how updates arrive, or the reason as the error line. */
export function claudeStatusView(read: StatusRead<HarnessStatus>, name: string, now: number = Date.now()): StatusView {
  const s = read.data;
  const problem = s === undefined ? null : reasonOf(s);
  const events = s === undefined || problem !== null ? null : (claudeDetails(s)?.events ?? null);
  const lines = [s === undefined || problem !== null ? "" : claudeIdentity(s), events === null ? "" : `updates ${updatesText(events, now).text}`].filter((l) => l !== "");
  return { summary: claudeSummary(read, name), detail: { lines, error: problem ?? read.error } };
}
