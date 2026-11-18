import { Link } from "react-router";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FormEvent, type KeyboardEvent } from "react";
import { useApi } from "../platform/context.tsx";
import { usePreferences, useInvalidateWork, useProjects, useSchedulePreview } from "../api/queries.ts";
import {
  INTERACTION_MODES,
  type CreateScheduleRequest,
  type CreateTaskRequest,
  type InteractionMode,
  type ModelDefaults,
  type Preferences,
  type Schedule,
  type StartMode as TreeStartMode,
  type Task,
  type UpdateTaskRequest,
} from "../api/types.ts";
import { parseTags } from "../lib/format.ts";
import {
  DEFAULT_REPEAT,
  REPEAT_PRESETS,
  WEEKDAYS,
  browserTimeZone,
  buildCron,
  defaultStartLocal,
  localToInstant,
  parseCron,
  repeatSummary,
  timeZones,
  toLocalInput,
  until,
  validTimeZone,
  wallTime,
  type RepeatPreset,
  type RepeatSpec,
} from "../lib/schedules.ts";
import { handoffHint } from "../lib/handoff.ts";
import { choiceToWorkspace, effectiveChoice, predecessorName, workspaceChoice, workspaceOptions, type WorkspaceChoice } from "../lib/launchbase.ts";
import { isFullyEditable } from "../lib/tasks.ts";
import { HandoffToggle } from "./Handoff.tsx";
import { ImageDropzone, ImageStrip, useImageSet } from "./Images.tsx";
import { ProjectCombobox } from "./ProjectSelect.tsx";
import { ModelFields, type ModelConfig } from "./ModelFields.tsx";
import { SnippetTextarea } from "./SnippetPicker.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Alert, Button, Chip, Drawer, ErrorText, Field, Input, Muted, Segmented, Select, Textarea, errorText } from "./ui.tsx";

/** How a saved task starts: right away, parked, at a time, or as a recurring schedule. */
export const START_MODES = ["now", "backlog", "at", "repeat"] as const;
export type StartMode = (typeof START_MODES)[number];

const START_OPTIONS: Record<StartMode, { value: StartMode; label: string; short: string }> = {
  now: { value: "now", label: "Now", short: "Now" },
  backlog: { value: "backlog", label: "Backlog", short: "Backlog" },
  at: { value: "at", label: "At time", short: "At" },
  repeat: { value: "repeat", label: "Repeat", short: "Repeat" },
};

const PRESET_LABELS: Record<RepeatPreset, string> = { hourly: "Hourly", daily: "Daily", weekdays: "Weekdays", weekly: "Weekly", cron: "Cron" };

// Phone cells are a fifth of 343px; "Weekdays" would clip, so it shortens.
const PRESET_OPTIONS: readonly { value: RepeatPreset; label: string; short?: string }[] = REPEAT_PRESETS.map((p) =>
  p === "weekdays" ? { value: p, label: PRESET_LABELS[p], short: "Mon-Fri" } : { value: p, label: PRESET_LABELS[p] },
);

/** Weekday picker order: Monday first. */
const WEEKDAY_ORDER = [1, 2, 3, 4, 5, 6, 0];

interface FormState {
  projectId: string;
  title: string;
  prompt: string;
  config: ModelConfig;
  interactionMode: InteractionMode;
  /** root, worktree from baseRef, or worktree from a need's branch (baseRef the fallback). */
  workspaceType: WorkspaceChoice;
  baseRef: string;
  branch: string;
  tags: string;
  notes: string;
  mode: StartMode;
  /** Append each need's handoff at launch (a task with needs). */
  handoff: boolean;
  /** Tree member's own start mode; inherit follows the tree. */
  treeStart: TreeStartChoice;
  /** One-shot wall time "YYYY-MM-DDTHH:mm" in tz. */
  startLocal: string;
  repeat: RepeatSpec;
  /** IANA zone for the start time or the repeat. */
  tz: string;
}

type DefaultsSource = "pinned" | "last used";

/** What the drawer saved: a task, or (Repeat) a schedule. */
export type Saved = { kind: "task"; task: Task } | { kind: "schedule"; schedule: Schedule };

/** New-task model prefill: pinned default, else the last created task's config. */
function pickDefaults(p: Preferences | undefined): { defaults: ModelDefaults; source: DefaultsSource } | null {
  if (p?.pinned != null) return { defaults: p.pinned, source: "pinned" };
  if (p?.lastUsed != null) return { defaults: p.lastUsed, source: "last used" };
  return null;
}

/** The task-shaped config shared by tasks and schedules. */
type Template = Pick<
  Task,
  | "projectId"
  | "title"
  | "prompt"
  | "providerInstanceId"
  | "model"
  | "modelOptions"
  | "runtimeMode"
  | "interactionMode"
  | "workspace"
  | "tags"
>;

function fromTemplate(t: Template): Omit<FormState, "mode" | "handoff" | "treeStart" | "startLocal" | "repeat" | "tz"> {
  return {
    projectId: String(t.projectId),
    title: t.title,
    prompt: t.prompt,
    config: {
      providerInstanceId: t.providerInstanceId,
      model: t.model,
      modelOptions: t.modelOptions,
      runtimeMode: t.runtimeMode,
    },
    interactionMode: t.interactionMode,
    workspaceType: workspaceChoice(t.workspace),
    baseRef: t.workspace.type === "worktree" ? t.workspace.baseRef : "origin/main",
    branch: t.workspace.type === "worktree" ? (t.workspace.branch ?? "") : "",
    tags: t.tags.join(", "),
    notes: "",
  };
}

/** New task joining a tree (tree view Add task): the tree and the members it needs. */
export interface TreePrefill {
  treeId: number;
  treeTitle: string;
  needs: number[];
  /** The tree's start mode (names what Inherit means). */
  startMode?: TreeStartMode;
}

type TreeStartChoice = "inherit" | TreeStartMode;

/** Start mode select options; Inherit names the tree's mode when known. */
function treeStartOptions(inherited: TreeStartMode | null): { value: TreeStartChoice; label: string }[] {
  return [
    { value: "inherit", label: inherited === null ? "Inherit from tree" : `Inherit (tree: ${inherited})` },
    { value: "auto", label: "Auto: starts when its needs are completed" },
    { value: "manual", label: "Manual: reads ready, you start it" },
  ];
}

/** Start options for a new task: a task with needs waits in backlog; tree tasks never repeat (schedules take no tree). */
function newModes(tree: TreePrefill | undefined): readonly StartMode[] {
  if (tree === undefined) return START_MODES;
  return tree.needs.length > 0 ? ["backlog"] : ["now", "backlog", "at"];
}

function initialState(
  task: Task | undefined,
  schedule: Schedule | undefined,
  defaultProjectId: number | undefined,
  defaultMode: StartMode | undefined,
  defaults: ModelDefaults | undefined,
  tree: TreePrefill | undefined,
): FormState {
  const tz = browserTimeZone();
  const treeStart: TreeStartChoice = task?.startMode ?? "inherit";
  const timing = { handoff: true, treeStart, startLocal: defaultStartLocal(tz), repeat: DEFAULT_REPEAT, tz };
  if (schedule !== undefined) {
    return { ...fromTemplate(schedule), notes: schedule.notes, ...timing, mode: "repeat", repeat: parseCron(schedule.cron), tz: schedule.tz };
  }
  if (task !== undefined) {
    const startTz = task.startTz ?? tz;
    // Start timing is a backlog choice; other states keep "backlog" so it never drives the label or validation.
    const mode: StartMode = task.state === "backlog" && task.startAt !== null ? "at" : "backlog";
    return {
      ...fromTemplate(task),
      ...timing,
      mode,
      handoff: task.handoff,
      startLocal: task.startAt === null ? defaultStartLocal(startTz) : toLocalInput(task.startAt, startTz),
      tz: startTz,
    };
  }
  return {
    ...timing,
    projectId: defaultProjectId === undefined ? "" : String(defaultProjectId),
    title: "",
    prompt: "",
    config:
      defaults === undefined
        ? { providerInstanceId: null, model: null, modelOptions: [], runtimeMode: "full-access" }
        : {
            providerInstanceId: defaults.providerInstanceId,
            model: defaults.model,
            modelOptions: defaults.modelOptions,
            runtimeMode: defaults.runtimeMode,
          },
    interactionMode: defaults?.interactionMode ?? "default",
    workspaceType: "root",
    baseRef: "origin/main",
    branch: "",
    tags: "",
    notes: "",
    mode: defaultMode ?? newModes(tree)[0] ?? "now",
  };
}

const INTERACTION_OPTIONS = INTERACTION_MODES.map((m) => ({ value: m, label: m }));

/** Value that settles `ms` after its last change (preview requests while typing a cron). */
function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}

interface TaskFormProps {
  /** Edit this task. */
  task?: Task;
  /** Edit this schedule (Repeat only). */
  schedule?: Schedule;
  defaultProjectId?: number;
  /** New task: preselected Start option (e.g. "repeat" for New schedule). */
  defaultMode?: StartMode;
  /** New task: join this tree, needing these members. */
  defaultTree?: TreePrefill;
  onClose: () => void;
  onSaved?: (saved: Saved) => void;
}

/** Create (no `task`/`schedule`) or edit form in a drawer. A new one waits for preferences (usually cached) to prefill its model config. */
export function TaskFormModal(props: TaskFormProps) {
  const prefs = usePreferences();
  if (props.task !== undefined || props.schedule !== undefined) return <TaskFormDrawer {...props} prefill={null} />;
  if (prefs.isPending) {
    return (
      <Drawer title={props.defaultMode === "repeat" ? "New schedule" : "New task"} onClose={props.onClose} wide>
        <Muted>Loading defaults…</Muted>
      </Drawer>
    );
  }
  return <TaskFormDrawer {...props} prefill={pickDefaults(prefs.data)} />;
}

function TaskFormDrawer({
  task,
  schedule,
  defaultProjectId,
  defaultMode,
  defaultTree,
  onClose,
  onSaved,
  prefill,
}: TaskFormProps & { prefill: { defaults: ModelDefaults; source: DefaultsSource } | null }) {
  const api = useApi();
  const [s, setS] = useState(() => initialState(task, schedule, defaultProjectId, defaultMode, prefill?.defaults, defaultTree));
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const projects = useProjects();
  const invalidate = useInvalidateWork();
  const selectedProject = projects.data?.projects.find((project) => String(project.id) === s.projectId);
  const bound = selectedProject?.bindings.some((binding) => binding.harness === "t3" && binding.status === "available") ?? false;
  const full = task === undefined || isFullyEditable(task);
  const images = useImageSet(task?.attachments ?? schedule?.attachments);
  const isNew = task === undefined && schedule === undefined;
  // Start options: all for a new task, timing only for a backlog task without needs, none otherwise (or for a schedule).
  const needs = task?.needs ?? [];
  const modes: readonly StartMode[] = isNew ? newModes(defaultTree) : task !== undefined && full && needs.length === 0 ? ["backlog", "at"] : [];
  const mode = s.mode;
  // The need a worktree can stack on (editing a task with needs); baseTaskId is kept from the task.
  const taskWs = task?.workspace;
  const baseTaskId = taskWs?.type === "worktree" ? (taskWs.baseTaskId ?? null) : null;
  const predName = task === undefined ? null : predecessorName(task);
  const wsChoice = effectiveChoice(s.workspaceType, predName);
  const snippetProject = s.projectId === "" ? null : Number(s.projectId);
  // A tree member (or a new task joining a tree) picks its own start mode; backlog only.
  const inTree = isNew ? defaultTree !== undefined : task !== undefined && task.treeId !== null && full;
  const inheritedMode = isNew ? (defaultTree?.startMode ?? null) : task !== undefined && task.startMode === null ? task.effectiveStartMode : null;

  const tzValid = validTimeZone(s.tz);
  const startMs = mode === "at" && tzValid ? localToInstant(s.startLocal, s.tz) : null;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (mode !== "at") return;
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [mode]);
  const startPast = startMs !== null && startMs <= now;

  const cron = buildCron(s.repeat);
  const previewInput = useDebounced(mode === "repeat" && cron !== "" && tzValid ? { cron, tz: s.tz } : null, 300);
  const preview = useSchedulePreview(previewInput);
  const previewStale = previewInput === null || previewInput.cron !== cron || previewInput.tz !== s.tz;
  const previewError = mode === "repeat" && !previewStale && preview.isError ? errorText(preview.error) : null;

  function template() {
    return {
      projectId: Number(s.projectId),
      title: s.title.trim(),
      prompt: s.prompt,
      providerInstanceId: s.config.providerInstanceId,
      model: s.config.model,
      modelOptions: s.config.modelOptions,
      runtimeMode: s.config.runtimeMode,
      interactionMode: s.interactionMode,
      workspace: choiceToWorkspace(wsChoice, s.baseRef, s.branch, baseTaskId),
      tags: parseTags(s.tags),
    };
  }

  const save = useMutation({
    mutationFn: async (): Promise<Saved> => {
      if (mode === "repeat") {
        const body: CreateScheduleRequest = { ...template(), notes: s.notes, attachmentIds: images.ids, cron, tz: s.tz };
        const saved = schedule === undefined ? await api.createSchedule(body) : await api.updateSchedule(schedule.id, body);
        return { kind: "schedule", schedule: saved };
      }
      const timed = mode === "at" ? { startLocal: s.startLocal, startTz: s.tz } : null;
      if (task !== undefined) {
        const tags = parseTags(s.tags);
        const body: UpdateTaskRequest = full ? { ...template(), attachmentIds: images.ids } : { title: s.title.trim(), tags };
        if (full) {
          if (needs.length > 0 && s.handoff !== task.handoff) body.handoff = s.handoff;
          if (inTree && s.treeStart !== (task.startMode ?? "inherit")) body.startMode = s.treeStart === "inherit" ? null : s.treeStart;
          if (timed !== null) {
            body.startLocal = timed.startLocal;
            body.startTz = timed.startTz;
          } else if (task.startAt !== null) {
            body.startLocal = null;
            body.startTz = null;
          }
        }
        return { kind: "task", task: await api.updateTask(task.id, body) };
      }
      const body: CreateTaskRequest = {
        ...template(),
        initialNote: s.notes,
        ...(images.ids.length === 0 ? {} : { attachmentIds: images.ids }),
        ...(timed !== null ? timed : { start: mode === "now" }),
        ...(defaultTree === undefined ? {} : { treeId: defaultTree.treeId }),
        ...(defaultTree === undefined || defaultTree.needs.length === 0 ? {} : { needs: defaultTree.needs }),
        ...(defaultTree === undefined || s.treeStart === "inherit" ? {} : { startMode: s.treeStart }),
      };
      return { kind: "task", task: await api.createTask(body) };
    },
    onSuccess: async (saved) => {
      await invalidate();
      onSaved?.(saved);
      onClose();
    },
  });

  const missing = [
    s.projectId === "" && "project",
    s.title.trim() === "" && "title",
    s.prompt.trim() === "" && "prompt",
    wsChoice !== "root" && s.baseRef.trim() === "" && (wsChoice === "predecessor" ? "fallback base ref" : "base ref"),
    images.uploading && "images still uploading",
    images.failed && "remove failed images",
    (mode === "at" || mode === "repeat") && !tzValid && "time zone",
    mode === "at" && startMs === null && "start time",
    startPast && "a future start time",
    mode === "repeat" && cron === "" && "cron",
    previewError !== null && "a valid schedule",
  ].filter((m) => m !== false);

  const blocked = missing.length > 0 || save.isPending || (mode === "now" && !bound);
  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    if (!blocked) save.mutate();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLFormElement>) => {
    if (e.key !== "Enter" || !(e.ctrlKey || e.metaKey)) return;
    e.preventDefault();
    submit();
  };

  const title = schedule !== undefined ? "Edit schedule" : task !== undefined ? `Edit task #${task.id}` : mode === "repeat" ? "New schedule" : "New task";
  const startLabel = startMs === null ? null : wallTime(new Date(startMs).toISOString(), s.tz, { short: true, now });
  const primaryLabel =
    mode === "repeat"
      ? "Save schedule"
      : mode === "at" && startLabel !== null
        ? `Save · starts ${startLabel}`
        : task !== undefined
          ? "Save"
          : mode === "backlog"
            ? "Save to backlog"
            : "Save & start";

  const footer = (
    <>
      <span className="min-w-0 flex-1 font-mono text-meta-sm text-muted max-md:basis-full empty:hidden md:text-right">
        {missing.length > 0 ? `required: ${missing.join(", ")}` : null}
      </span>
      <Button variant="primary" busy={save.isPending} disabled={blocked} onClick={() => submit()} className="ml-auto">
        {primaryLabel}
        <kbd className="hidden border border-current/40 px-1 font-mono text-label leading-4 md:inline">Ctrl ↵</kbd>
      </Button>
    </>
  );

  return (
    <Drawer title={title} onClose={onClose} wide footer={footer}>
      <form onSubmit={submit} onKeyDown={onKeyDown} className="flex flex-col gap-3.5">
        {task === undefined || full ? null : (
          <Alert tone="plain">
            Task is <TaskStateBadge state={task.state} />; only title and tags can change. Add notes in its journal.
          </Alert>
        )}
        {!isNew || defaultTree === undefined ? null : (
          <Alert tone="plain">
            Joins tree <span className="font-medium">{defaultTree.treeTitle}</span>
            {defaultTree.needs.length === 0 ? (
              " as a root."
            ) : (
              <>
                , needing <span className="font-mono tabular">{defaultTree.needs.map((id) => `#${id}`).join(" ")}</span>. It waits
                in backlog until {defaultTree.needs.length === 1 ? "it is completed" : "they are completed"}.
              </>
            )}
          </Alert>
        )}
        {schedule === undefined ? null : (
          <Alert tone="plain">Changes apply to future runs. Tasks this schedule already started keep their config.</Alert>
        )}
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="Project">
            {(id) => (
              <ProjectCombobox
                id={id}
                projects={projects.data?.projects ?? []}
                disabled={!full}
                value={s.projectId}
                onChange={(projectId) => set({ projectId })}
                placeholder="Select project…"
                autoFocus={isNew}
              />
            )}
          </Field>
        </div>
        {selectedProject === undefined || bound ? null : <Alert>
          Planning is available. Bind or confirm a workspace before launching.
          <div className="mt-2 flex flex-wrap gap-3">
            <Link to={`/projects/${selectedProject.id}`} className="underline">Bind workspace</Link>
            {mode === "now" ? <Button small onClick={() => set({mode: "backlog"})}>Save to backlog instead</Button> : null}
          </div>
        </Alert>}
        <Field label="Title">
          {(id) => <Input id={id} data-autofocus={isNew ? undefined : true} value={s.title} onChange={(e) => set({ title: e.target.value })} />}
        </Field>
        <Field label="Prompt" hint={full ? "Type / to insert a snippet." : undefined}>
          {(id) =>
            full ? (
              <ImageDropzone set={images} label="Prompt images">
                <SnippetTextarea
                  id={id}
                  rows={8}
                  value={s.prompt}
                  onValueChange={(prompt) => set({ prompt })}
                  projectId={snippetProject}
                />
              </ImageDropzone>
            ) : (
              <div className="flex flex-col gap-2">
                <Textarea id={id} rows={8} disabled value={s.prompt} />
                <ImageStrip attachments={task?.attachments ?? []} label="Prompt images" />
              </div>
            )
          }
        </Field>
        <ModelFields value={s.config} onChange={(config) => set({ config })} disabled={!full} />
        <fieldset disabled={!full} className="grid min-w-0 grid-cols-1 gap-3 disabled:opacity-55 md:grid-cols-2">
          <div className="flex min-w-0 flex-col items-start gap-1.5">
            <span className="label-caps">Mode</span>
            <Segmented
              label="Interaction mode"
              options={INTERACTION_OPTIONS}
              value={s.interactionMode}
              onChange={(interactionMode) => set({ interactionMode })}
            />
          </div>
          <div className="flex min-w-0 flex-col items-start gap-1.5">
            <span className="label-caps">Workspace</span>
            <Segmented
              label="Workspace"
              options={workspaceOptions(predName)}
              value={wsChoice}
              onChange={(workspaceType) => set({ workspaceType })}
            />
          </div>
          {wsChoice !== "root" ? (
            <>
              <Field
                label={wsChoice === "predecessor" ? "Fallback base ref" : "Base ref"}
                hint={wsChoice === "predecessor" ? `Used when ${predName ?? "a need"} has no usable branch.` : undefined}
              >
                {(id) => <Input id={id} value={s.baseRef} onChange={(e) => set({ baseRef: e.target.value })} />}
              </Field>
              <Field label="Branch" hint={mode === "repeat" ? "Empty = T3 picks a name each run." : "Empty = T3 picks a name."}>
                {(id) => <Input id={id} value={s.branch} onChange={(e) => set({ branch: e.target.value })} />}
              </Field>
            </>
          ) : null}
        </fieldset>
        {prefill === null ? null : (
          <p className="-mt-1.5 font-mono text-meta-sm text-muted">
            {prefill.source === "pinned"
              ? "model and mode prefilled from your pinned default (Settings)"
              : "model and mode prefilled from your last task · pin a default in Settings"}
          </p>
        )}
        {modes.length > 0 ? (
          <div className="flex min-w-0 flex-col items-start gap-1.5">
            <span className="label-caps">Start</span>
            <Segmented label="Start" fill options={modes.map((m) => START_OPTIONS[m])} value={mode} onChange={(m) => set({ mode: m })} />
          </div>
        ) : null}
        {mode === "at" && modes.length > 0 ? (
          <StartAtFields s={s} set={set} startMs={startMs} past={startPast} now={now} />
        ) : null}
        {inTree ? (
          <Field label="Tree start" className="md:max-w-sm">
            {(id) => (
              <Select
                id={id}
                value={s.treeStart}
                onChange={(e) => {
                  const picked = treeStartOptions(inheritedMode).find((o) => o.value === e.target.value);
                  if (picked !== undefined) set({ treeStart: picked.value });
                }}
                className="w-full"
              >
                {treeStartOptions(inheritedMode).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        ) : null}
        {task !== undefined && full && needs.length > 0 ? (
          <HandoffToggle checked={s.handoff} onChange={(handoff) => set({ handoff })} hint={handoffHint(task)} />
        ) : null}
        {mode === "repeat" ? (
          <RepeatFields
            s={s}
            set={set}
            cron={cron}
            next={previewStale ? null : (preview.data?.next ?? null)}
            loading={preview.isFetching || (previewStale && previewInput !== null)}
            error={previewError}
          />
        ) : null}
        <Field
          label="Tags"
          hint={
            parseTags(s.tags).length === 0 ? (
              "Comma separated."
            ) : (
              <span className="flex flex-wrap gap-1">
                {parseTags(s.tags).map((t) => (
                  <Chip key={t}>#{t}</Chip>
                ))}
              </span>
            )
          }
        >
          {(id) => <Input id={id} value={s.tags} onChange={(e) => set({ tags: e.target.value })} placeholder="smoke, refactor" />}
        </Field>
        {task === undefined ? (
          <Field
            label={mode === "repeat" ? "Notes template" : "Initial note"}
            hint={mode === "repeat" ? "Starts each spawned task's journal." : "Optional. Appended once to the new task's journal."}
          >
            {(id) => (
              <SnippetTextarea id={id} rows={3} value={s.notes} onValueChange={(notes) => set({ notes })} projectId={snippetProject} />
            )}
          </Field>
        ) : null}
        <ErrorText error={save.error} />
      </form>
    </Drawer>
  );
}

type FieldsProps = { s: FormState; set: (patch: Partial<FormState>) => void };

function ZoneField({ s, set }: FieldsProps) {
  const zones = useMemo(() => timeZones(s.tz), [s.tz]);
  return (
    <Field label="Time zone">
      {(id) => (
        <Select id={id} value={s.tz} onChange={(e) => set({ tz: e.target.value })} className="w-full font-mono">
          {zones.map((z) => (
            <option key={z} value={z}>
              {z}
            </option>
          ))}
        </Select>
      )}
    </Field>
  );
}

function StartAtFields({ s, set, startMs, past, now }: FieldsProps & { startMs: number | null; past: boolean; now: number }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field label="Starts at">
          {(id) => (
            <Input
              id={id}
              type="datetime-local"
              value={s.startLocal}
              onChange={(e) => set({ startLocal: e.target.value })}
              className="font-mono"
            />
          )}
        </Field>
        <ZoneField s={s} set={set} />
      </div>
      <p className={past ? "font-mono text-meta text-bad" : "font-mono text-meta text-muted"} aria-live="polite">
        {startMs === null
          ? "Pick a date and time."
          : past
            ? "That time has passed. Pick a later one."
            : `starts in ${until(startMs, now)} · ${wallTime(new Date(startMs).toISOString(), s.tz, { zone: true, now })}`}
      </p>
    </div>
  );
}

const clampInt = (text: string, max: number): number => {
  const n = Number.parseInt(text, 10);
  return Number.isNaN(n) ? 0 : Math.min(max, Math.max(0, n));
};

function RepeatFields({
  s,
  set,
  cron,
  next,
  loading,
  error,
}: FieldsProps & { cron: string; next: string[] | null; loading: boolean; error: string | null }) {
  const r = s.repeat;
  const setRepeat = (patch: Partial<RepeatSpec>) => set({ repeat: { ...r, ...patch } });
  const time = `${String(r.hour).padStart(2, "0")}:${String(r.minute).padStart(2, "0")}`;
  const setTime = (value: string) => {
    const m = /^(\d{2}):(\d{2})/.exec(value);
    if (m !== null) setRepeat({ hour: clampInt(m[1] ?? "0", 23), minute: clampInt(m[2] ?? "0", 59) });
  };
  // Switching to Cron starts from the current preset's expression.
  const setPreset = (preset: RepeatPreset) => setRepeat(preset === "cron" && r.preset !== "cron" ? { preset, cron } : { preset });

  return (
    <div className="flex flex-col gap-3">
      <div className="flex min-w-0 flex-col items-start gap-1.5">
        <span className="label-caps">Repeat</span>
        <Segmented label="Repeat" fill options={PRESET_OPTIONS} value={r.preset} onChange={setPreset} />
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {r.preset === "hourly" ? (
          <Field label="Minute past the hour">
            {(id) => (
              <Input
                id={id}
                type="number"
                inputMode="numeric"
                min={0}
                max={59}
                value={r.minute}
                onChange={(e) => setRepeat({ minute: clampInt(e.target.value, 59) })}
                className="font-mono"
              />
            )}
          </Field>
        ) : null}
        {r.preset === "weekly" ? (
          <Field label="Day">
            {(id) => (
              <Select id={id} value={r.weekday} onChange={(e) => setRepeat({ weekday: clampInt(e.target.value, 6) })} className="w-full">
                {WEEKDAY_ORDER.map((d) => (
                  <option key={d} value={d}>
                    {WEEKDAYS[d]}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        ) : null}
        {r.preset === "daily" || r.preset === "weekdays" || r.preset === "weekly" ? (
          <Field label="Time">
            {(id) => <Input id={id} type="time" value={time} onChange={(e) => setTime(e.target.value)} className="font-mono" />}
          </Field>
        ) : null}
        {r.preset === "cron" ? (
          <Field label="Cron" hint="minute hour day-of-month month day-of-week">
            {(id) => (
              <Input
                id={id}
                value={r.cron}
                onChange={(e) => setRepeat({ cron: e.target.value })}
                placeholder="0 */6 * * *"
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                className="font-mono"
              />
            )}
          </Field>
        ) : null}
        <ZoneField s={s} set={set} />
      </div>
      <div className="flex flex-col gap-0.5 border border-line bg-panel px-2.5 py-1.5 font-mono text-meta" aria-live="polite">
        {error !== null ? (
          <p className="text-bad">{error}</p>
        ) : (
          <>
            <p className="text-fg">
              {repeatSummary(r)} <span className="text-muted">· {s.tz}</span>
            </p>
            <p className="text-muted">
              {next === null || next.length === 0
                ? loading
                  ? "next: checking…"
                  : "next: -"
                : `next: ${next.map((iso) => wallTime(iso, s.tz)).join(" · ")}`}
            </p>
          </>
        )}
      </div>
    </div>
  );
}
