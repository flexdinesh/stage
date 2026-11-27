import { useMutation } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FormEvent, type KeyboardEvent } from "react";
import { useApi } from "../platform/context.tsx";
import { useConnectionCapabilities, usePreferences, useInvalidateWork, useProjects, useSchedulePreview } from "../api/queries.ts";
import {
  type ExecutionConfig,
  type Schedule,
  type StartMode as TreeStartMode,
  type Task,
} from "../api/types.ts";
import {
  DEFAULT_REPEAT,
  REPEAT_PRESETS,
  WEEKDAYS,
  browserTimeZone,
  buildCron,
  defaultStartLocal,
  localToInstant,
  parseCron,
  parsePreviewKey,
  previewKey,
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
import { isFullyEditable } from "../lib/tasks.ts";
import { blockedStartModes, scheduleBody, scheduleUpdateBody, taskCreateBody, taskUpdateBody, taskWorkspaceLocation, type TaskTemplate, type TreeStartChoice } from "../lib/taskForm.ts";
import { operationReason } from "../lib/launch.ts";
import { HandoffToggle } from "./Handoff.tsx";
import { LaunchBlockAlert, useOperationGate } from "./LaunchGate.tsx";
import { ImageDropzone, ImageStrip, useImageSet } from "./Images.tsx";
import { ProjectCombobox } from "./ProjectSelect.tsx";
import { HarnessFields, harnessDefaults, incompatibleSettings } from "./HarnessFields.tsx";
import { prefillConfig } from "../lib/capabilities.ts";
import { SnippetTextarea } from "./SnippetPicker.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { TaskDestinationFields, useTaskDestination } from "./TaskDestination.tsx";
import {
  Alert,
  Button,
  Drawer,
  ErrorText,
  Field,
  FieldGroup,
  Hint,
  InlineError,
  Input,
  Meta,
  MetaLine,
  Muted,
  Segmented,
  Textarea,
  errorText,
  Combobox,
} from "./ui.tsx";

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

/** What the drawer saved: a task, or (Repeat) a schedule. */
export type Saved = { kind: "task"; task: Task } | { kind: "schedule"; schedule: Schedule };

/** The task-shaped config shared by tasks and schedules. */
type Template = Pick<
  Task,
  | "projectId"
  | "title"
  | "prompt"
>;

function fromTemplate(t: Template): Omit<FormState, "mode" | "handoff" | "treeStart" | "startLocal" | "repeat" | "tz"> {
  return {
    projectId: String(t.projectId),
    title: t.title,
    prompt: t.prompt,
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
    notes: "",
    mode: defaultMode ?? newModes(tree)[0] ?? "now",
  };
}


/** Value updated after `ms` without a change, for previews while typing a cron. */
function useDebounced<T extends string | null>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return debounced;
}

interface TaskFormProps {
  /** Edit this task. */
  task?: Task;
  /** Start a new backlog draft from this task. */
  duplicate?: Task;
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

/** Create (no `task`/`schedule`) or edit form in a drawer. A new one waits for preferences (usually cached) to start on the default connection. */
export function TaskFormModal(props: TaskFormProps) {
  const prefs = usePreferences();
  if (props.task !== undefined || props.schedule !== undefined || props.duplicate !== undefined) return <TaskFormDrawer {...props} defaultConnection={null} />;
  if (prefs.isPending) {
    return (
      <Drawer title={props.defaultMode === "repeat" ? "New schedule" : "New task"} onClose={props.onClose} wide>
        <Muted>Loading defaults…</Muted>
      </Drawer>
    );
  }
  return <TaskFormDrawer {...props} defaultConnection={prefs.data?.defaultConnectionId ?? null} />;
}

function TaskFormDrawer({
  task,
  schedule,
  duplicate,
  defaultProjectId,
  defaultMode,
  defaultTree,
  onClose,
  onSaved,
  defaultConnection,
}: TaskFormProps & { defaultConnection: number | null }) {
  const api = useApi();
  const [copySource] = useState(duplicate);
  const [s, setS] = useState(() => copySource === undefined
    ? initialState(task, schedule, defaultProjectId, defaultMode, defaultTree)
    : { ...initialState(undefined, undefined, copySource.projectId, "backlog", undefined), ...fromTemplate(copySource) });
  // The task and form state as the form opened: a save sends only what differs from them.
  const [opened] = useState(() => (task === undefined ? null : { task, state: s }));
  const [openedSchedule] = useState(schedule);
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const invalidate = useInvalidateWork();
  const existing = opened?.task ?? openedSchedule;
  const full = task === undefined || isFullyEditable(task);
  const source = existing ?? copySource;
  const savedId = source?.workspaceId || null;
  const savedWorkspace = opened?.task.workspace ?? copySource?.workspace;
  const destination = useTaskDestination({
    initialConnection: source?.connectionId ?? defaultConnection,
    initialWorkspace: savedId === null ? null : {
      id: savedId, label: "Saved workspace", detail: savedWorkspace == null ? "" : taskWorkspaceLocation(savedWorkspace),
    },
    allowPrefill: source === undefined,
    projectId: defaultProjectId,
    enabled: full,
  });
  const { connection, workspaceId, selected, connectionName } = destination;
  const bound = selected !== undefined;
  const destinationChanged = source === undefined || source.connectionId !== connection || savedId !== workspaceId;
  const [keepOriginalConfig, setKeepOriginalConfig] = useState(true);
  const [draftConfig, setConfig] = useState<ExecutionConfig | null>(null);
  const prefilled = (source === undefined || !keepOriginalConfig) && draftConfig === null ? prefillConfig(destination.picked) : undefined;
  const original = keepOriginalConfig && source !== undefined ? source.executionConfig : prefilled;
  const effectiveConfig = draftConfig ?? (original == null ? harnessDefaults : { ...original, options: original.options ?? [] });
  const configEdited = destinationChanged || draftConfig !== null;
  const caps = useConnectionCapabilities(connection, full && bound, selected === undefined ? undefined : { workspaceId: selected.workspaceId });
  const issues = bound && (configEdited || copySource !== undefined) ? incompatibleSettings(effectiveConfig, caps.data) : [];
  const acceptsImages = caps.data === undefined || caps.data.operations.images?.available === true;
  const images = useImageSet(source?.attachments);
  const isNew = existing === undefined;
  // The API verifies images when the destination changes or when a draft adds an image.
  const checksImages =
    existing === undefined ||
    destinationChanged ||
    images.ids.some((id) => !existing.attachments?.some((attachment) => attachment.id === id));
  // Start options: all for a new task, timing only for a backlog task without needs, none otherwise (or for a schedule).
  const needs = task?.needs ?? [];
  const modes: readonly StartMode[] = copySource !== undefined || defaultMode === "repeat" ? []
    : isNew ? newModes(defaultTree) : task !== undefined && full && needs.length === 0 ? ["backlog", "at"] : [];
  const operationGate = useOperationGate();
  const block = bound && full ? operationGate(connection, "launch") : null;
  const scopedBlock = bound && full ? operationReason("launch", caps.data, caps.error) : null;
  const launchable = bound && block === null && scopedBlock === null;
  // Still reading the chosen workspace's launch support: the Workspace line says so, so the form does not move. Only
  // a final answer (unsupported or an error) takes a Hint row of its own.
  const checkingLaunch = block === null && scopedBlock !== null && caps.data === undefined && caps.error === null;
  const mode = s.mode;
  const timingUnchanged = opened !== null && mode === "at" && opened.state.mode === "at"
    && s.startLocal === opened.state.startLocal && s.tz === opened.state.tz;
  const requiresLaunch = mode === "now" || (mode === "at" && (!timingUnchanged || destinationChanged))
    || (mode === "repeat" && (isNew || destinationChanged));
  const offModes = blockedStartModes(modes, !launchable, mode);
  const planning = full && mode === "backlog" && !bound;
  const chosenProject = s.projectId === "" ? undefined : Number(s.projectId);
  const workspaceProject = selected?.groupProjectId ?? undefined;
  const projectId = existing?.projectId ?? (planning || (copySource !== undefined && !destinationChanged) ? chosenProject
    : workspaceProject);
  const snippetProject = projectId ?? null;
  const showPlanningProject = isNew && planning;
  const projects = useProjects("false", { enabled: showPlanningProject });
  // A tree member (or a new task joining a tree) picks its own start mode; backlog only.
  const inTree = isNew ? defaultTree !== undefined : task !== undefined && task.treeId !== null && full;
  const inheritedMode = isNew ? (defaultTree?.startMode ?? null) : task !== undefined && task.startMode === null ? task.effectiveStartMode : null;
  const { tzValid, startMs, now, startPast, cron, previewInput, preview, previewStale, previewError } = useStartTiming({ ...s, mode });

  function resetConfig() {
    setKeepOriginalConfig(false);
    setConfig(null);
  }

  function template(): TaskTemplate {
    return {
      ...(projectId === undefined ? {} : { projectId }),
      title: s.title.trim(),
      prompt: s.prompt,
      connectionId: connection,
      ...(bound ? { workspaceId: selected.workspaceId } : isNew || destinationChanged ? { workspaceId: null } : {}),
      ...(isNew || existing.executionConfig != null || configEdited
        ? { executionConfig: !bound && (isNew || destinationChanged) ? harnessDefaults : effectiveConfig } : {}),
    };
  }

  const save = useMutation({
    mutationFn: async (): Promise<Saved> => {
      if (copySource !== undefined) {
        const body = { ...template(), workspaceId: selected?.workspaceId ?? null, attachmentIds: images.ids };
        return { kind: "task", task: await api.duplicateTask(copySource.id, body) };
      }
      if (mode === "repeat") {
        const body = scheduleBody(template(), { notes: s.notes, attachmentIds: images.ids, cron, tz: s.tz });
        if (openedSchedule === undefined) return { kind: "schedule", schedule: await api.createSchedule(body) };
        const patch = scheduleUpdateBody(openedSchedule, body);
        const saved = Object.keys(patch).length === 0 ? openedSchedule : await api.updateSchedule(openedSchedule.id, patch);
        return { kind: "schedule", schedule: saved };
      }
      const timed = mode === "at" ? { startLocal: s.startLocal, startTz: s.tz } : null;
      if (task !== undefined) {
        const body = taskUpdateBody({
          before: opened?.task ?? task,
          openedStart: opened?.state ?? null,
          full,
          template: template(),
          attachmentIds: images.ids,
          handoff: needs.length > 0 ? s.handoff : null,
          treeStart: inTree ? s.treeStart : null,
          timed,
        });
        // Nothing changed: no request, so nothing on the server is touched.
        if (Object.keys(body).length === 0) return { kind: "task", task };
        return { kind: "task", task: await api.updateTask(task.id, body) };
      }
      const body = taskCreateBody(template(), {
        notes: s.notes,
        attachmentIds: images.ids,
        timed,
        startNow: mode === "now",
        tree: defaultTree,
        treeStart: s.treeStart,
      });
      return { kind: "task", task: await api.createTask(body) };
    },
  });

  const missing = [
    showPlanningProject && projectId === undefined && "project for backlog",
    s.title.trim() === "" && "title",
    full && s.prompt.trim() === "" && "prompt",
    full && (isNew || destinationChanged) && !destination.canPlan && "connection",
    full && requiresLaunch && !bound && "workspace",
    full && requiresLaunch && bound && !launchable && "available launch connection",
    full && !bound && workspaceId !== null && (isNew || destinationChanged) && "choose or clear workspace",
    full && issues.length > 0 && `resolve ${issues.join(", ")}`,
    full && checksImages && !acceptsImages && images.items.length > 0 && "remove images",
    images.uploading && "images still uploading",
    images.failed && "remove failed images",
    (mode === "at" || mode === "repeat") && !tzValid && "time zone",
    mode === "at" && startMs === null && "start time",
    startPast && !timingUnchanged && "a future start time",
    mode === "repeat" && cron === "" && "cron",
    previewError !== null && "a valid schedule",
  ].filter((m) => m !== false);

  const blocked = missing.length > 0 || save.isPending;
  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    if (!blocked) save.mutate(undefined, { onSuccess: (saved) => {
      void invalidate();
      onSaved?.(saved);
      onClose();
    } });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLFormElement>) => {
    if (e.key !== "Enter" || !(e.ctrlKey || e.metaKey)) return;
    e.preventDefault();
    submit();
  };

  const title = copySource !== undefined ? `Duplicate task #${copySource.id}` : schedule !== undefined ? "Edit schedule"
    : task !== undefined ? `Edit task #${task.id}` : mode === "repeat" ? "New schedule" : "New task";
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
      <Meta small className="min-w-0 flex-1 max-md:basis-full empty:hidden md:text-right">
        {missing.length > 0 ? `required: ${missing.join(", ")}` : null}
      </Meta>
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
            Task is <TaskStateBadge state={task.state} />; only the title can change. Add notes in its journal.
          </Alert>
        )}
        {!isNew || defaultTree === undefined ? null : (
          <Alert tone="plain">
            Joins tree <span className="font-medium">{defaultTree.treeTitle}</span>
            {defaultTree.needs.length === 0 ? (
              " as a parallel root: it waits on nothing. To run it after a task, add it from that task."
            ) : (
              <>
                , running after <span className="font-mono tabular">{defaultTree.needs.map((id) => `#${id}`).join(" ")}</span>. It waits
                in backlog until {defaultTree.needs.length === 1 ? "it is completed" : "they are completed"}.
              </>
            )}
          </Alert>
        )}
        {schedule === undefined ? null : (
          <Alert tone="plain">Changes apply to future runs. Tasks this schedule already started keep their config.</Alert>
        )}
        <TaskDestinationFields
          destination={destination}
          readOnly={!full}
          canClear={mode === "backlog"}
          checkingLaunch={checkingLaunch}
          backlogNeedsWorkspace={planning && workspaceId === null}
          onConnection={(id) => {
            destination.chooseConnection(id);
            resetConfig();
          }}
          onWorkspace={(id) => {
            destination.chooseWorkspace(id);
            resetConfig();
          }}
        />
        {showPlanningProject ? (
          <Field label="Project">
            {(id) => <ProjectCombobox id={id} value={s.projectId} projects={projects.data?.projects ?? []}
              onChange={(pickedId) => set({ projectId: pickedId })} placeholder="Choose a project…" />}
          </Field>
        ) : null}
        {showPlanningProject ? <ErrorText error={projects.error} /> : null}
        {block === null ? null : <LaunchBlockAlert block={block} />}
        {bound && block === null && scopedBlock !== null && !checkingLaunch ? <Hint>{scopedBlock}</Hint> : null}
        <Field label="Title">
          {(id) => <Input id={id} data-autofocus={full ? undefined : true} value={s.title} onChange={(e) => set({ title: e.target.value })} />}
        </Field>
        <Field label="Prompt" hint={full ? "Type / to insert a snippet." : undefined}>
          {(id) =>
            full ? (
              <div className="flex min-w-0 flex-col gap-2">
                <ImageDropzone set={images} label="Prompt images" disabled={!acceptsImages}>
                  <SnippetTextarea
                    id={id}
                    rows={8}
                    value={s.prompt}
                    onValueChange={(prompt) => set({ prompt })}
                    projectId={snippetProject}
                  />
                </ImageDropzone>
                {!acceptsImages && images.items.length > 0 ? (
                  <Alert>
                    <span className="break-words">
                      {caps.data?.operations.images?.reason || "This harness cannot receive these images."}
                    </span>
                  </Alert>
                ) : null}
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                <Textarea id={id} rows={8} disabled value={s.prompt} />
                <ImageStrip attachments={task?.attachments ?? []} label="Prompt images" />
              </div>
            )
          }
        </Field>
        {full && bound ? (
          <HarnessFields
            title={`Harness · ${connectionName}`}
            config={effectiveConfig}
            caps={caps.data}
            onChange={setConfig}
            disabled={!full}
            allowReset={full}
            loading={caps.live && caps.data === undefined && (caps.isPending || caps.isFetching)}
            error={caps.isFetching ? null : caps.error}
            onRetry={() => void caps.refetch()}
          />
        ) : null}
        {!bound || prefilled === undefined ? null : (
          <Hint className="-mt-1.5">Model and options use this connection’s defaults.</Hint>
        )}
        {modes.length > 0 ? (
          <FieldGroup label="Start">
            <div className="md:self-start">
              <Segmented
                label="Start"
                fill
                options={modes.map((m) => ({ ...START_OPTIONS[m], disabled: offModes.includes(m) }))}
                value={mode}
                onChange={(m) => set({ mode: m })}
              />
            </div>
          </FieldGroup>
        ) : null}
        {mode === "at" && modes.length > 0 ? (
          <StartAtFields s={s} set={set} startMs={startMs} past={startPast} now={now} />
        ) : null}
        {inTree ? (
          <Field label="Tree start" className="md:max-w-sm">
            {(id) => (
              <Combobox
                id={id}
                value={s.treeStart}
                onChange={(next) => {
                  const picked = treeStartOptions(inheritedMode).find((o) => o.value === next);
                  if (picked !== undefined) set({ treeStart: picked.value });
                }}
                listLabel="Tree start modes"
                className="w-full"
                options={treeStartOptions(inheritedMode)}
              />
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
        {task === undefined && copySource === undefined ? (
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

/** When the task starts: a start time (validity, whether it passed, a clock that ticks while it shows) or a repeat (its cron and debounced preview). */
function useStartTiming(s: FormState) {
  const mode = s.mode;
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
  const previewInput = parsePreviewKey(useDebounced(mode === "repeat" && cron !== "" && tzValid ? previewKey(cron, s.tz) : null, 300));
  const preview = useSchedulePreview(previewInput);
  const previewStale = previewInput === null || previewInput.cron !== cron || previewInput.tz !== s.tz;
  const previewError = mode === "repeat" && !previewStale && preview.isError ? errorText(preview.error) : null;
  return { tzValid, startMs, now, startPast, cron, previewInput, preview, previewStale, previewError };
}

type FieldsProps = { s: FormState; set: (patch: Partial<FormState>) => void };

function ZoneField({ s, set }: FieldsProps) {
  const zones = useMemo(() => timeZones(s.tz), [s.tz]);
  return (
    <Field label="Time zone">
      {(id) => (
        <Combobox
          id={id}
          value={s.tz}
          onChange={(tz) => set({ tz })}
          listLabel="Time zones"
          className="w-full font-mono"
          options={zones.map((z) => ({ value: z, label: z }))}
        />
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
      <div aria-live="polite">
        {startMs === null ? (
          <Hint>Pick a date and time.</Hint>
        ) : past ? (
          <InlineError>That time has passed. Pick a later one.</InlineError>
        ) : (
          <MetaLine>
            {`starts in ${until(startMs, now)}`}
            {wallTime(new Date(startMs).toISOString(), s.tz, { zone: true, now })}
          </MetaLine>
        )}
      </div>
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
      <FieldGroup label="Repeat">
        <div className="md:self-start">
          <Segmented label="Repeat" fill options={PRESET_OPTIONS} value={r.preset} onChange={setPreset} />
        </div>
      </FieldGroup>
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
              <Combobox
                id={id}
                value={String(r.weekday)}
                onChange={(day) => setRepeat({ weekday: clampInt(day, 6) })}
                listLabel="Weekdays"
                className="w-full"
                options={WEEKDAY_ORDER.flatMap((d) => {
                  const label = WEEKDAYS[d];
                  return label === undefined ? [] : [{ value: String(d), label }];
                })}
              />
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
          <InlineError>{error}</InlineError>
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
