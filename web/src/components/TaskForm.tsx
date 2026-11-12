import { useMutation } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FormEvent, type KeyboardEvent } from "react";
import { api } from "../api/client.ts";
import { usePreferences, useInvalidateWork, useProjects, useSchedulePreview } from "../api/queries.ts";
import {
  INTERACTION_MODES,
  type CreateScheduleRequest,
  type CreateTaskRequest,
  type InteractionMode,
  type ModelDefaults,
  type Preferences,
  type Schedule,
  type Task,
  type UpdateTaskRequest,
  type Workspace,
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
import { isFinal, isFullyEditable } from "../lib/tasks.ts";
import { ImageDropzone, ImageStrip, useImageSet } from "./Images.tsx";
import { ProjectCombobox } from "./ProjectSelect.tsx";
import { ModelFields, type ModelConfig } from "./ModelFields.tsx";
import { TaskPicker, useLinkCandidates } from "./Sequence.tsx";
import { SnippetTextarea } from "./SnippetPicker.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Alert, Button, Chip, Drawer, ErrorText, Field, Input, Muted, Segmented, Select, Textarea, errorText } from "./ui.tsx";

/** How a saved task starts: right away, parked, at a time, after another task, or as a recurring schedule. */
export const START_MODES = ["now", "backlog", "at", "after", "repeat"] as const;
export type StartMode = (typeof START_MODES)[number];

const START_OPTIONS: Record<StartMode, { value: StartMode; label: string; short: string }> = {
  now: { value: "now", label: "Now", short: "Now" },
  backlog: { value: "backlog", label: "Backlog", short: "Backlog" },
  at: { value: "at", label: "At time", short: "At" },
  after: { value: "after", label: "After task", short: "After" },
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
  workspaceType: Workspace["type"];
  baseRef: string;
  branch: string;
  tags: string;
  notes: string;
  mode: StartMode;
  /** Runs-after predecessor id ("" none). */
  afterTaskId: string;
  /** Follower id ("" none). */
  nextTaskId: string;
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
  | "notes"
>;

function fromTemplate(t: Template): Omit<FormState, "mode" | "afterTaskId" | "nextTaskId" | "startLocal" | "repeat" | "tz"> {
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
    workspaceType: t.workspace.type,
    baseRef: t.workspace.type === "worktree" ? t.workspace.baseRef : "origin/main",
    branch: t.workspace.type === "worktree" ? (t.workspace.branch ?? "") : "",
    tags: t.tags.join(", "),
    notes: t.notes,
  };
}

function initialState(
  task: Task | undefined,
  schedule: Schedule | undefined,
  defaultProjectId: number | undefined,
  defaultMode: StartMode | undefined,
  defaults: ModelDefaults | undefined,
): FormState {
  const tz = browserTimeZone();
  const timing = { afterTaskId: "", nextTaskId: "", startLocal: defaultStartLocal(tz), repeat: DEFAULT_REPEAT, tz };
  if (schedule !== undefined) {
    return { ...fromTemplate(schedule), ...timing, mode: "repeat", repeat: parseCron(schedule.cron), tz: schedule.tz };
  }
  if (task !== undefined) {
    const startTz = task.startTz ?? tz;
    // Start timing is a backlog choice; other states keep "backlog" so it never drives the label or validation.
    const mode: StartMode = task.state !== "backlog" ? "backlog" : task.startAt !== null ? "at" : task.afterTaskId !== null ? "after" : "backlog";
    return {
      ...fromTemplate(task),
      ...timing,
      mode,
      afterTaskId: task.afterTaskId === null ? "" : String(task.afterTaskId),
      nextTaskId: task.nextTaskId === null ? "" : String(task.nextTaskId),
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
    mode: defaultMode ?? "now",
  };
}

const WORKSPACE_OPTIONS: readonly { value: Workspace["type"]; label: string }[] = [
  { value: "root", label: "root" },
  { value: "worktree", label: "worktree" },
];

const INTERACTION_OPTIONS = INTERACTION_MODES.map((m) => ({ value: m, label: m }));

function toWorkspace(s: FormState): Workspace {
  if (s.workspaceType === "root") return { type: "root" };
  const branch = s.branch.trim();
  return { type: "worktree", baseRef: s.baseRef.trim(), branch: branch === "" ? null : branch };
}

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
  onClose,
  onSaved,
  prefill,
}: TaskFormProps & { prefill: { defaults: ModelDefaults; source: DefaultsSource } | null }) {
  const [s, setS] = useState(() => initialState(task, schedule, defaultProjectId, defaultMode, prefill?.defaults));
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const projects = useProjects();
  const invalidate = useInvalidateWork();
  const full = task === undefined || isFullyEditable(task);
  const images = useImageSet(task?.attachments ?? schedule?.attachments);
  const candidates = useLinkCandidates(task);
  const isNew = task === undefined && schedule === undefined;
  // Start options: all for a new task, timing only for a backlog task, none otherwise (or for a schedule).
  const modes: readonly StartMode[] = isNew ? START_MODES : task !== undefined && full ? ["backlog", "at", "after"] : [];
  const mode = s.mode;
  // Then run: any task that can still gate (not settled/cancelled).
  const canLinkAfter = task !== undefined && !isFinal(task);
  const after = mode === "after" && s.afterTaskId !== "" ? Number(s.afterTaskId) : null;
  const snippetProject = s.projectId === "" ? null : Number(s.projectId);

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
      workspace: toWorkspace(s),
      tags: parseTags(s.tags),
      notes: s.notes,
    };
  }

  const save = useMutation({
    mutationFn: async (): Promise<Saved> => {
      if (mode === "repeat") {
        const body: CreateScheduleRequest = { ...template(), attachmentIds: images.ids, cron, tz: s.tz };
        const saved = schedule === undefined ? await api.createSchedule(body) : await api.updateSchedule(schedule.id, body);
        return { kind: "schedule", schedule: saved };
      }
      const timed = mode === "at" ? { startLocal: s.startLocal, startTz: s.tz } : null;
      if (task !== undefined) {
        const tags = parseTags(s.tags);
        const body: UpdateTaskRequest = full ? { ...template(), attachmentIds: images.ids } : { title: s.title.trim(), tags, notes: s.notes };
        if (full) {
          if (after !== task.afterTaskId) body.afterTaskId = after;
          if (timed !== null) {
            body.startLocal = timed.startLocal;
            body.startTz = timed.startTz;
          } else if (task.startAt !== null) {
            body.startLocal = null;
            body.startTz = null;
          }
        }
        const saved = await api.updateTask(task.id, body);
        return { kind: "task", task: await relinkNext(task, saved) };
      }
      const body: CreateTaskRequest = {
        ...template(),
        ...(images.ids.length === 0 ? {} : { attachmentIds: images.ids }),
        ...(after !== null ? { afterTaskId: after } : timed !== null ? timed : { start: mode === "now" }),
      };
      return { kind: "task", task: await api.createTask(body) };
    },
    onSuccess: async (saved) => {
      await invalidate();
      onSaved?.(saved);
      onClose();
    },
  });

  // "Then run" edits the follower's link: unlink the old one, link the new one.
  async function relinkNext(self: Task, saved: Task): Promise<Task> {
    const next = s.nextTaskId === "" ? null : Number(s.nextTaskId);
    if (!canLinkAfter || next === self.nextTaskId) return saved;
    if (self.nextTaskId !== null) await api.updateTask(self.nextTaskId, { afterTaskId: null });
    if (next !== null) await api.updateTask(next, { afterTaskId: self.id });
    return saved;
  }

  const missing = [
    s.projectId === "" && "project",
    s.title.trim() === "" && "title",
    s.prompt.trim() === "" && "prompt",
    s.workspaceType === "worktree" && s.baseRef.trim() === "" && "base ref",
    images.uploading && "images still uploading",
    images.failed && "remove failed images",
    (mode === "at" || mode === "repeat") && !tzValid && "time zone",
    mode === "at" && startMs === null && "start time",
    startPast && "a future start time",
    mode === "after" && after === null && "task to run after",
    mode === "repeat" && cron === "" && "cron",
    previewError !== null && "a valid schedule",
  ].filter((m) => m !== false);

  const blocked = missing.length > 0 || save.isPending;
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
        : mode === "after" && after !== null
          ? `Save · runs after #${after}`
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
            Task is <TaskStateBadge state={task.state} />; only title, tags and notes can change.
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
              options={WORKSPACE_OPTIONS}
              value={s.workspaceType}
              onChange={(workspaceType) => set({ workspaceType })}
            />
          </div>
          {s.workspaceType === "worktree" ? (
            <>
              <Field label="Base ref">
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
        {mode === "after" && modes.length > 0 ? (
          <Field label="Runs after" hint={after === null ? "Starts when that task settles in T3." : "Saved waiting; starts when that task settles in T3."}>
            {(id) => (
              <TaskPicker
                id={id}
                value={s.afterTaskId}
                onChange={(afterTaskId) => set({ afterTaskId })}
                options={candidates.before}
                none="Pick a task…"
              />
            )}
          </Field>
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
        {canLinkAfter ? (
          <Field label="Then run" hint="A backlog task that starts when this one settles.">
            {(id) => (
              <TaskPicker
                id={id}
                value={s.nextTaskId}
                onChange={(nextTaskId) => set({ nextTaskId })}
                options={candidates.after}
                none="Nothing"
              />
            )}
          </Field>
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
        <Field label="Notes">
          {(id) => (
            <SnippetTextarea id={id} rows={3} value={s.notes} onValueChange={(notes) => set({ notes })} projectId={snippetProject} />
          )}
        </Field>
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
