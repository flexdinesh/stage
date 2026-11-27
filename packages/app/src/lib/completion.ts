import type { CompletionPreview, HarnessDescriptor, Provenance, Task } from "../api/types.ts";
import { relativeTime } from "./format.ts";
import { statusChips, waitTarget, type StatusChip } from "./tasks.ts";

type Dependent = CompletionPreview["dependents"][number];

/** Dependents completing the task would start: unclaimed auto-mode ones, while the task's tree (if any) is open. */
export function autoStarts(preview: Pick<CompletionPreview, "dependents">, task: Pick<Task, "treeStatus">): Dependent[] {
  if (task.treeStatus !== null && task.treeStatus !== "open") return [];
  return preview.dependents.filter((d) => !d.claimed && d.startMode === "auto");
}

/** "Starts #12, #13" (ids deduplicated, first-seen order); "" when nothing starts. */
export function startsText(dependents: readonly Pick<Dependent, "taskId">[]): string {
  const ids = [...new Set(dependents.map((d) => d.taskId))];
  return ids.length === 0 ? "" : `Starts ${ids.map((id) => `#${id}`).join(", ")}`;
}

/** The task's harness closure that produced an inferred completion's provenance. */
const closureFor = (provenance: Provenance | null, harness: Pick<HarnessDescriptor, "closures"> | undefined) =>
  harness?.closures.find((c) => c.provenance === provenance);

/** Accepted result line: who completed it and what stays fixed. `harness` is the task's harness descriptor (copy). */
export function provenanceText(provenance: Provenance, harness?: Pick<HarnessDescriptor, "closures">): string {
  switch (provenance) {
    case "historical_settlement":
      return "Migrated from historical T3 settlement. Evidence reflects the local cache at migration.";
    case "user":
      return "Completed in Stage. This result stays unchanged when execution continues.";
    default:
      return closureFor(provenance, harness)?.completed ?? "Completed automatically when its harness closed the session. This result stays unchanged when execution continues.";
  }
}

/** How an inferred completion happened, after "completed 3h ago" from the harness descriptor. */
export function autoCompletedMeta(provenance: Provenance | null, harness?: Pick<HarnessDescriptor, "closures">): string | null {
  if (provenance === null || provenance === "user" || provenance === "historical_settlement") return null;
  return closureFor(provenance, harness)?.via ?? "via harness";
}

type StateTask = Pick<Task, "state" | "readiness" | "acceptanceId" | "needs" | "startAt" | "completedAt" | "finishedAt" | "completionProvenance">;

/**
 * The one manual state change a task offers (the State picker's second option): Completed for open work, disabled
 * while it runs; for completed work, back to the status it would show without acceptance ("to review", "ready"...).
 */
export type StateMove =
  | { kind: "complete"; to: "completed"; detail: string; disabled: boolean }
  | { kind: "uncomplete"; to: StatusChip; detail: string; disabled: false };

type MoveTask = Pick<Task, "state" | "readiness" | "acceptanceId" | "treeId" | "treeStatus" | "adopted" | "needs" | "startAt" | "effectiveStartMode">;

/** The task's State move. `accepted` holds the tree members with current acceptance (the server clears a completed task's readiness). */
export function stateMove(t: MoveTask, accepted: ReadonlySet<number>): StateMove {
  if (t.acceptanceId !== null) {
    const to = statusChips({ state: t.state, readiness: readinessWithout(t, accepted), acceptanceId: null })[0] ?? t.state;
    return { kind: "uncomplete", to, detail: "clears acceptance", disabled: false };
  }
  if (isActiveState(t.state)) return { kind: "complete", to: "completed", detail: "stop execution first", disabled: true };
  const detail =
    t.state === "backlog" ? "accept without a run" : t.state === "done" ? "review, then accept" : "accept as it ended";
  return { kind: "complete", to: "completed", detail, disabled: false };
}

/** Readiness once acceptance clears, as the server computes it (`readinessSQL`). */
function readinessWithout(t: MoveTask, accepted: ReadonlySet<number>): Task["readiness"] {
  if (t.treeId === null || t.state !== "backlog" || t.adopted || (t.treeStatus !== null && t.treeStatus !== "open")) return null;
  if (t.needs.some((id) => !accepted.has(id))) return "blocked";
  return t.startAt === null && (t.effectiveStartMode === "manual" || t.needs.length === 0) ? "ready" : null;
}

const isActiveState = (s: Task["state"]) => s === "launching" || s === "running" || s === "needs_input";

/** One line under the State picker: what the current state means. `harness` is the task's descriptor (closure copy). */
export function stateSummary(t: StateTask, now: number = Date.now(), harness?: Pick<HarnessDescriptor, "closures">): string {
  if (t.acceptanceId !== null) {
    const via = autoCompletedMeta(t.completionProvenance, harness);
    const accepted = t.completedAt === null ? "Accepted" : `Accepted ${relativeTime(t.completedAt, now)}`;
    const moved = statusChips(t).length > 1 ? " Execution moved on since." : "";
    return `${accepted}${via === null ? "" : ` ${via}`}.${moved}`;
  }
  switch (t.state) {
    case "backlog":
      if (t.readiness === "blocked") return `Waiting on ${waitTarget(t)}.`;
      if (t.readiness === "ready" && t.needs.length > 0) return "Needs met. Not run yet.";
      return t.startAt === null ? "Not run yet." : "Scheduled. Not run yet.";
    case "launching":
      return "Launch not confirmed yet.";
    case "running":
      return "Running. Stop it before completing.";
    case "needs_input":
      return "Waiting for your input in the harness.";
    case "done":
      return t.finishedAt === null ? "Run finished." : `Run finished ${relativeTime(t.finishedAt, now)}.`;
    case "failed":
    case "interrupted":
    case "cancelled":
      return `Run ${t.state}.`;
  }
}
