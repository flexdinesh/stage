// Request bodies the task form (components/TaskForm.tsx) saves: a new task, a task edit, or a schedule.
import type { CreateScheduleRequest, CreateTaskRequest, ExecutionConfig, Schedule, StartMode, Task, TaskWorkspace, UpdateScheduleRequest, UpdateTaskRequest, Workspace, WorkspaceInventory } from "../api/types.ts";
import { changedFields } from "./changes.ts";
import { buildCron, parseCron } from "./schedules.ts";
import { taskEditBaseline, taskEditChanges } from "./tasks.ts";

/** The task-shaped config shared by task and schedule forms. */
export interface TaskTemplate {
  projectId?: number;
  title: string;
  prompt: string;
  connectionId: number;
  workspaceId?: string | null;
  executionConfig?: ExecutionConfig;
}

type SelectableWorkspace = Workspace & { workspaceId: string };

export function availableTaskWorkspaces(inventory: WorkspaceInventory | undefined, connectionId: number, projectId: number | undefined) {
  if (inventory?.inventory?.state !== "ready") return [];
  const inProject = (row: Workspace) => Number(row.groupProjectId === projectId);
  return inventory.workspaces.filter((row): row is SelectableWorkspace => (
    row.connectionId === connectionId && row.available && !row.harnessArchived && !!row.workspaceId
  )).toSorted((a, b) => inProject(b) - inProject(a) || a.title.localeCompare(b.title) || a.workspace.localeCompare(b.workspace));
}

export function suggestedTaskWorkspace(rows: readonly SelectableWorkspace[], projectId: number | undefined) {
  const candidates = projectId === undefined ? rows : rows.filter((row) => row.groupProjectId === projectId);
  return candidates.length === 1 ? candidates[0] : undefined;
}

/**
 * The one line always kept under an editable Workspace field, so the form never moves: the destination status, else
 * the launch check, else the path (set in mono), else what is missing.
 */
export function workspaceLine(s: {
  status: string | null;
  path: string | null;
  checkingLaunch: boolean;
  backlogNeedsWorkspace: boolean;
  hasConnection: boolean;
}): { text: string; isPath: boolean } {
  if (s.status !== null) return { text: s.status, isPath: false };
  if (s.checkingLaunch) return { text: "Checking launch support…", isPath: false };
  if (s.path !== null) return { text: s.path, isPath: true };
  if (s.backlogNeedsWorkspace) return { text: "Choose a workspace before starting this backlog task.", isPath: false };
  return { text: s.hasConnection ? "No workspace selected" : "No connection selected", isPath: false };
}

/** Display the assignment from the task itself, including pathless destinations. */
export function taskWorkspaceLocation(workspace: TaskWorkspace): string {
  return workspace.path || workspace.externalId || `Binding #${workspace.bindingId}`;
}

/** A tree member's start mode choice; inherit takes the tree's. */
export type TreeStartChoice = "inherit" | StartMode;

/** A one-shot start time, or null for none. */
export type TimedStart = { startLocal: string; startTz: string } | null;

export function scheduleBody(template: TaskTemplate, s: { notes: string; attachmentIds: number[]; cron: string; tz: string }): CreateScheduleRequest {
  const { workspaceId, ...fields } = template;
  return { ...fields, ...(workspaceId == null ? {} : { workspaceId }), notes: s.notes, attachmentIds: s.attachmentIds, cron: s.cron, tz: s.tz };
}

/** A schedule edit sends only changed fields, so other edits keep the saved destination and repeat. */
export function scheduleUpdateBody(before: Schedule, next: CreateScheduleRequest): UpdateScheduleRequest {
  const body = changedFields<UpdateScheduleRequest>({
    ...taskEditBaseline(before),
    notes: before.notes,
    cron: buildCron(parseCron(before.cron)),
    tz: before.tz,
  }, next);
  if ((body.connectionId !== undefined || body.workspaceId !== undefined) && next.executionConfig !== undefined) {
    body.executionConfig = next.executionConfig;
  }
  return body;
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
  const baseline = taskEditBaseline(s.before);
  const body = taskEditChanges(baseline, { ...s.template, attachmentIds: s.attachmentIds });
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

/**
 * Start options a form disables while its harness cannot launch (launchBlock): every one but Backlog, except `keep`,
 * the timing an edited task opened with, so a title edit never clears its start time.
 */
export function blockedStartModes<M extends string>(modes: readonly M[], blocked: boolean, keep: M | null): M[] {
  return blocked ? modes.filter((m) => m !== "backlog" && m !== keep) : [];
}
