import type { SteeringEndReason, SteeringEvent, TaskHold, TaskState, TreeSteerer } from "../api/types.ts";
import { relativeTime } from "./format.ts";
import { mintRequestId } from "./workspaceAdd.ts";

// Tree steering presentation (ADR 0013): the steerer's phase, its delivery line, held needs-input copy, the hold
// presets and the steering feed's lines. Pure: callers pass `now`.

/**
 * Where a steerer stands. `live`: it takes updates and member waits are held for it (running, done, needs input).
 * `starting`: not launched yet. `stopped`: failed, interrupted or cancelled; needs input goes to the user.
 */
export type SteererPhase = "starting" | "live" | "stopped";

export function steererPhase(state: TaskState): SteererPhase {
  if (state === "running" || state === "done" || state === "needs_input") return "live";
  if (state === "backlog" || state === "launching") return "starting";
  return "stopped";
}

/** The server's lastError while the steerer can't take updates; the stopped phase already says it. */
const NOT_RUNNING = "steerer not running";

/** The steerer's delivery line: "Last update 2m ago · 3 pending" ("No update sent yet" before the first digest). */
export function deliveryLine(s: Pick<TreeSteerer, "lastDeliveredAt" | "pending">, now: number = Date.now()): string {
  const last = s.lastDeliveredAt === null ? "No update sent yet" : `Last update ${relativeTime(s.lastDeliveredAt, now)}`;
  return `${last} · ${s.pending} pending`;
}

/** The last delivery error worth showing: none, or a stopped steerer's "not running" (its phase says so). */
export function deliveryError(s: Pick<TreeSteerer, "lastError" | "state">): string | null {
  if (s.lastError === null || s.lastError.trim() === "") return null;
  if (s.lastError === NOT_RUNNING && steererPhase(s.state) !== "live") return null;
  return s.lastError;
}

/** Time left on a hold in whole minutes, counting up a started minute ("4m"); under a minute, or past due while the server releases it, "<1m". */
export function holdCountdown(until: string, now: number = Date.now()): string {
  const ms = new Date(until).getTime() - now;
  if (Number.isNaN(ms) || ms <= 60_000) return "<1m";
  return `${Math.ceil(ms / 60_000)}m`;
}

const steererName = (hold: TaskHold) => (hold.steererTaskId === null ? "The steerer" : `Steerer #${hold.steererTaskId}`);

/** A held task row's chip: "Steerer · 4m", titled with who handles it and when the user hears about it. */
export function heldChip(hold: TaskHold, now: number = Date.now()): { label: string; title: string } {
  const left = holdCountdown(hold.until, now);
  return { label: `Steerer · ${left}`, title: `${steererName(hold)} is handling this. You will be notified in ${left}.` };
}

/** Task detail's needs-input alert while held. */
export function heldAlert(hold: TaskHold, now: number = Date.now()): string {
  return `${steererName(hold)} is handling this. You'll be notified in ${holdCountdown(hold.until, now)} if it doesn't answer.`;
}

/** Hold presets offered in the tree's More menu: off, 2, 5, 10 and 15 minutes. */
export const HOLD_PRESETS: readonly number[] = [0, 120, 300, 600, 900];

/** "Off", "5 min", "90 s" (a value set elsewhere that is not whole minutes). */
export function holdLabel(seconds: number): string {
  if (seconds <= 0) return "Off";
  return seconds % 60 === 0 ? `${seconds / 60} min` : `${seconds} s`;
}

/** The hold menu's choices: the presets plus the current value when it is not one (set through the CLI or MCP), in order. */
export function holdOptions(current: number): { seconds: number; label: string; checked: boolean }[] {
  const values = HOLD_PRESETS.includes(current) ? HOLD_PRESETS : [...HOLD_PRESETS, current].toSorted((a, b) => a - b);
  return values.map((seconds) => ({ seconds, label: holdLabel(seconds), checked: seconds === current }));
}

const END_REASONS: Record<SteeringEndReason, string> = {
  replaced: "replaced",
  stopped: "stopped",
  tree_finished: "tree finished",
  task_removed: "steerer task removed",
};

const isEndReason = (reason: string): reason is SteeringEndReason => Object.hasOwn(END_REASONS, reason);

export function endReasonLabel(reason: string): string {
  return isEndReason(reason) ? END_REASONS[reason] : reason.replaceAll("_", " ");
}

const FINISH_REASONS: Record<string, string> = {
  members_completed: "every member completed",
  tree_done: "tree marked done",
  tree_archived: "tree archived",
};

const text = (payload: Record<string, unknown>, key: string): string | null => {
  const v = payload[key];
  return typeof v === "string" && v.trim() !== "" ? v : null;
};

const KIND_LABELS: Record<string, string> = {
  member_needs_input: "needs input",
  member_done: "run finished",
  member_failed: "failed",
  member_interrupted: "interrupted",
  member_cancelled: "cancelled",
  member_ready: "ready",
  member_added: "joined the tree",
  member_removed: "left the tree",
  member_accepted: "completed",
  member_reopened: "reopened",
  member_answered: "answered",
  hold_escalated: "you were notified",
  tree_status: "tree status",
  steering_started: "steering started",
  steering_ended: "steering ended",
  tree_finished: "tree finished",
};

/** One feed line: what happened (`label`), the member's title, and a detail line (reply, error, reason), each optional. */
export function steeringEventLine(e: Pick<SteeringEvent, "kind" | "payload">): { label: string; title: string | null; detail: string | null } {
  const p = e.payload;
  const label = e.kind === "note" ? `${text(p, "category") ?? "note"} note` : (KIND_LABELS[e.kind] ?? e.kind.replaceAll("_", " "));
  let detail: string | null = null;
  switch (e.kind) {
    case "member_done":
      detail = text(p, "reply");
      break;
    case "member_failed":
    case "member_interrupted":
      detail = text(p, "error");
      break;
    case "member_answered":
      detail = text(p, "via") === "respond" ? "through a request" : "through a message";
      break;
    case "note":
      detail = text(p, "text");
      break;
    case "hold_escalated": {
      const reason = text(p, "reason");
      detail = p["timeout"] === true ? "Steerer did not answer" : reason === null ? null : `Steerer: ${reason}`;
      break;
    }
    case "tree_status": {
      const from = text(p, "from");
      const to = text(p, "to");
      detail = from === null || to === null ? null : `${from} to ${to}`;
      break;
    }
    case "steering_ended": {
      const reason = text(p, "reason");
      detail = reason === null ? null : endReasonLabel(reason);
      break;
    }
    case "tree_finished": {
      const reason = text(p, "reason");
      detail = reason === null ? null : (FINISH_REASONS[reason] ?? reason.replaceAll("_", " "));
      break;
    }
  }
  return { label, title: text(p, "title"), detail };
}

/**
 * Whether a feed event still waits for a digest: after the active binding's cursor and not the steerer's own. Ids are
 * monotonic per tree and a binding's cursor starts at its own steering_started, so everything after it is this binding's.
 */
export function eventPending(e: Pick<SteeringEvent, "id" | "self">, active: Pick<TreeSteerer, "deliveredThrough"> | null): boolean {
  return active !== null && !e.self && e.id > active.deliveredThrough;
}

const JOURNAL_LABELS: Record<string, string> = {
  steering_started: "steering started",
  steering_replaced: "steerer replaced",
  steering_ended: "steering ended",
  steering_hold_changed: "steerer hold",
};

/** A tree journal event's label: steering events in words, others their type with spaces. */
export function journalEventLabel(eventType: string): string {
  return JOURNAL_LABELS[eventType] ?? eventType.replaceAll("_", " ");
}

/** A Go duration as written in hold events ("5m0s", "1h0m0s") in short form ("5m", "1h"). */
const shortDuration = (d: string) => d.replace(/(\d+h)0m0s\b/g, "$1").replace(/(\d+m)0s\b/g, "$1");

/**
 * A tree journal event's text in words: "Steering by #12 ended (tree_finished)" reads "Steering by #12 ended: tree
 * finished"; a hold change "Steering hold: 5m to off". Other events keep their text.
 */
export function journalEventText(eventType: string, value: string): string {
  if (eventType === "steering_ended") return value.replace(/ \(([a-z_]+)\)$/, (_, reason: string) => `: ${endReasonLabel(reason)}`);
  if (eventType === "steering_hold_changed") return shortDuration(value).replace(" -> ", " to ");
  return value;
}

/** A retry key for POST /steerer: the server replays an identical request and refuses the key for other content. */
export const mintSteerRequestId = (): string => mintRequestId(undefined, "steer");
