// Request bodies the task form (components/TaskForm.tsx) saves: a new task, a task edit, or a schedule.
import type { CreateScheduleRequest, CreateTaskRequest, ExecutionConfig, StartMode, Task, UpdateTaskRequest, Workspace } from "../api/types.ts";
import { choiceToWorkspace, type WorkspaceChoice } from "./launchbase.ts";
import { taskEditBaseline, taskEditChanges } from "./tasks.ts";

/** The fields every form body carries. */
export interface TaskTemplate {
  projectId: number;
  title: string;
  prompt: string;
  connectionId: number;
  executionConfig: ExecutionConfig;
  workspace: Workspace;
}

/** A tree member's start mode choice; inherit takes the tree's. */
export type TreeStartChoice = "inherit" | StartMode;

/** A one-shot start time, or null for none. */
export type TimedStart = { startLocal: string; startTz: string } | null;

/** The workspace a form saves. A harness that picks its own worktree base and branch gets neither from Stage. */
export function formWorkspace(choice: WorkspaceChoice, baseRef: string, branch: string, baseTaskId: number | null, harnessWorktree: boolean): Workspace {
  return harnessWorktree && choice === "worktree" ? { type: "worktree", baseRef: "", branch: null } : choiceToWorkspace(choice, baseRef, branch, baseTaskId);
}

export function scheduleBody(template: TaskTemplate, s: { notes: string; attachmentIds: number[]; cron: string; tz: string }): CreateScheduleRequest {
  return { ...template, notes: s.notes, attachmentIds: s.attachmentIds, cron: s.cron, tz: s.tz };
}

export function taskCreateBody(
  template: TaskTemplate,
  s: {
    notes: string;
    attachmentIds: number[];
    timed: TimedStart;
    /** Start right away (the Now option); ignored with a start time. */
    startNow: boolean;
    tree: { treeId: number; needs: number[] } | undefined;
    treeStart: TreeStartChoice;
  },
): CreateTaskRequest {
  return {
    ...template,
    initialNote: s.notes,
    ...(s.attachmentIds.length === 0 ? {} : { attachmentIds: s.attachmentIds }),
    ...(s.timed !== null ? s.timed : { start: s.startNow }),
    ...(s.tree === undefined ? {} : { treeId: s.tree.treeId }),
    ...(s.tree === undefined || s.tree.needs.length === 0 ? {} : { needs: s.tree.needs }),
    ...(s.tree === undefined || s.treeStart === "inherit" ? {} : { startMode: s.treeStart }),
  };
}

/**
 * A task edit body, compared with the task and form as they opened so untouched fields never revert another
 * client's change. A task past backlog changes only its title.
 */
export function taskUpdateBody(s: {
  /** The task as the form opened. */
  before: Task;
  /** The form's start fields as it opened. */
  openedStart: { mode: string; startLocal: string; tz: string } | null;
  full: boolean;
  template: TaskTemplate;
  attachmentIds: number[];
  /** Set when the form edits it (a task with needs), else null. */
  handoff: boolean | null;
  /** Set when the form edits it (a tree member), else null. */
  treeStart: TreeStartChoice | null;
  timed: TimedStart;
}): UpdateTaskRequest {
  if (!s.full) return { title: s.template.title };
  const body = taskEditChanges(taskEditBaseline(s.before), { ...s.template, attachmentIds: s.attachmentIds });
  if (s.handoff !== null && s.handoff !== s.before.handoff) body.handoff = s.handoff;
  if (s.treeStart !== null && s.treeStart !== (s.before.startMode ?? "inherit")) body.startMode = s.treeStart === "inherit" ? null : s.treeStart;
  if (s.timed !== null) {
    const o = s.openedStart;
    const sameStart = o !== null && o.mode === "at" && o.startLocal === s.timed.startLocal && o.tz === s.timed.startTz;
    if (!sameStart) {
      body.startLocal = s.timed.startLocal;
      body.startTz = s.timed.startTz;
    }
  } else if (s.before.startAt !== null) {
    body.startLocal = null;
    body.startTz = null;
  }
  return body;
}
