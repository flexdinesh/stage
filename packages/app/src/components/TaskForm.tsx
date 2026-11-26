import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FormEvent, type KeyboardEvent } from "react";
import { useApi } from "../platform/context.tsx";
import { useHarnesses, useMachines, usePreferences, useInvalidateWork, useProjects, useSchedulePreview } from "../api/queries.ts";
import { connectionOptions, machineTag, withMachine } from "../lib/machines.ts";
import {
  type ExecutionConfig,
  type OperationId,
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
import { blockedStartModes, scheduleBody, taskCreateBody, taskUpdateBody, type TaskTemplate, type TreeStartChoice } from "../lib/taskForm.ts";
import { projectsForConnection } from "../lib/projects.ts";
import { HandoffToggle } from "./Handoff.tsx";
import { LaunchBlockAlert, useLaunchGate } from "./LaunchGate.tsx";
import { ImageDropzone, ImageStrip, useImageSet } from "./Images.tsx";
import { ProjectCombobox } from "./ProjectSelect.tsx";
import { HarnessFields, harnessDefaults, incompatibleSettings } from "./HarnessFields.tsx";
import { newTaskConnection, prefillConfig } from "../lib/capabilities.ts";
import { SnippetTextarea } from "./SnippetPicker.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
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
  TextLink,
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


/** Value that settles `ms` after its last change (preview requests while typing a cron). Pass a primitive: a new object each render never settles. */
function useDebounced<T extends string | null>(value: T, ms: number): T {
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

/** Create (no `task`/`schedule`) or edit form in a drawer. A new one waits for preferences (usually cached) to start on the default connection. */
export function TaskFormModal(props: TaskFormProps) {
  const prefs = usePreferences();
  if (props.task !== undefined || props.schedule !== undefined) return <TaskFormDrawer {...props} defaultConnection={null} />;
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
  defaultProjectId,
  defaultMode,
  defaultTree,
  onClose,
  onSaved,
  defaultConnection,
}: TaskFormProps & { defaultConnection: number | null }) {
  const api = useApi();
  const [s, setS] = useState(() => initialState(task, schedule, defaultProjectId, defaultMode, defaultTree));
  // The task and form state as the form opened: a save sends only what differs from them.
  const [opened] = useState(() => (task === undefined ? null : { task, state: s }));
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const invalidate = useInvalidateWork();
  const { machines } = useMachines();
  const existing = task ?? schedule;
  const {
    harnesses,
    selectedProject,
    bindings,
    chosen,
    connection,
    pickable,
    bound,
    caps,
    acceptsImages,
    prefilled,
    draftConfig,
    setDraftConfig,
    effectiveConfig,
    issues,
    unverifiedConfig,
    chooseHarness,
    fellBack,
  } = useHarnessConfig(existing, s.projectId, defaultConnection);
  const full = task === undefined || isFullyEditable(task);
  const images = useImageSet(task?.attachments ?? schedule?.attachments);
  const isNew = task === undefined && schedule === undefined;
  // Start options: all for a new task, timing only for a backlog task without needs, none otherwise (or for a schedule).
  const needs = task?.needs ?? [];
  const modes: readonly StartMode[] = isNew ? newModes(defaultTree) : task !== undefined && full && needs.length === 0 ? ["backlog", "at"] : [];
  // A harness that cannot launch now leaves Backlog (and an edited task's own timing); the choice returns with it.
  const launchGate = useLaunchGate();
  const block = selectedProject === undefined || connection === 0 ? null : launchGate({ connectionId: connection, projectId: selectedProject.id, projectTitle: selectedProject.title });
  const offModes = blockedStartModes(modes, block !== null, opened === null ? null : opened.state.mode);
  const mode: StartMode = offModes.includes(s.mode) ? "backlog" : s.mode;
  const snippetProject = s.projectId === "" ? null : Number(s.projectId);
  // A tree member (or a new task joining a tree) picks its own start mode; backlog only.
  const inTree = isNew ? defaultTree !== undefined : task !== undefined && task.treeId !== null && full;
  const inheritedMode = isNew ? (defaultTree?.startMode ?? null) : task !== undefined && task.startMode === null ? task.effectiveStartMode : null;
  const { tzValid, startMs, now, startPast, cron, previewInput, preview, previewStale, previewError } = useStartTiming({ ...s, mode });
  // "T3 · desk" once there is more than one machine, else "T3".
  const connectionName = (id: number) => withMachine(harnesses.name(id), machineTag(machines, harnesses.connection(id)?.machineId ?? 0));

  function template(): TaskTemplate {
    return {
      projectId: Number(s.projectId),
      title: s.title.trim(),
      prompt: s.prompt,
      connectionId: connection,
      executionConfig: effectiveConfig,
    };
  }

  const save = useMutation({
    mutationFn: async (): Promise<Saved> => {
      if (mode === "repeat") {
        const body = scheduleBody(template(), { notes: s.notes, attachmentIds: images.ids, cron, tz: s.tz });
        const saved = schedule === undefined ? await api.createSchedule(body) : await api.updateSchedule(schedule.id, body);
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
    // Closes at once; the views behind the drawer refresh in the background.
    onSuccess: (saved) => {
      void invalidate();
      onSaved?.(saved);
      onClose();
    },
  });

  const missing = [
    s.projectId === "" && "project",
    s.title.trim() === "" && "title",
    full && s.prompt.trim() === "" && "prompt",
    full && connection === 0 && "harness",
    full && issues.length > 0 && `resolve ${issues.join(", ")}`,
    full && unverifiedConfig && "verify or clear saved settings",
    full && !acceptsImages && images.items.length > 0 && "remove images",
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
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="Project">
            {(id) => (
              <ProjectCombobox
                id={id}
                projects={pickable}
                disabled={!full}
                value={s.projectId}
                // The list holds only projects the chosen harness can use, so the choice stands.
                onChange={(projectId) => set({projectId})}
                placeholder="Select project…"
                autoFocus={isNew}
                emptyText={chosen === 0 || pickable.length > 0 ? undefined : <NoHarnessProjects connectionId={chosen} />}
              />
            )}
          </Field>
          <Field label="Harness">{id=><Combobox id={id} disabled={!full} value={connection ? String(connection) : ""} onChange={next=>chooseHarness(Number(next))} listLabel="Harnesses" showTag options={[
            {value: "", label: "Choose a harness"},
            // Machine > Harness: the machine is the tag once there is more than one; a chosen project's bindings list first.
            ...connectionOptions(harnesses.connectedList, machines, harnesses.kindName, selectedProject === undefined ? null : new Set(bindings.map(b => b.connectionId)), defaultConnection),
            // The current harness of a task or schedule that can no longer launch shows, disabled, so the field is not blank.
            ...(connection > 0 && harnesses.retired(connection) ? [{value: String(connection), label: `${harnesses.name(connection)} (replaced)`, disabled: true}] : []),
            ...(connection > 0 && harnesses.unconfigured(connection) ? [{value: String(connection), label: `${harnesses.name(connection)} (disconnected)`, disabled: true}] : []),
          ]}/>}</Field>
        </div>
        {fellBack === null || selectedProject === undefined ? null : (
          <Hint className="-mt-1.5">
            {selectedProject.title} is not on {connectionName(fellBack)} (default).{" "}
            {connection ? `Using ${connectionName(connection)}.` : "Choose a harness."}
          </Hint>
        )}
        <ErrorText error={harnesses.error}/>
        {block === null || !full ? null : (
          <LaunchBlockAlert block={block} {...(modes.includes("backlog") && mode === "backlog" ? { after: "Saves to backlog until then." } : {})} />
        )}
        <Field label="Title">
          {(id) => <Input id={id} data-autofocus={isNew ? undefined : true} value={s.title} onChange={(e) => set({ title: e.target.value })} />}
        </Field>
        <Field label="Prompt" hint={full ? "Type / to insert a snippet." : undefined}>
          {(id) =>
            full ? (
              <ImageDropzone set={images} label="Prompt images" disabled={!acceptsImages}>
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
        {connection ? <>
          <HarnessFields title={`Harness · ${connectionName(connection)}`} config={effectiveConfig} caps={caps.data} onChange={setDraftConfig} disabled={!full}/>
          <ErrorText error={caps.error}/>
          {unverifiedConfig ? <p className="text-small">Connect and bind this harness to verify saved choices, or clear each unavailable choice above.</p> : null}
          {!acceptsImages && images.items.length ? <Alert>{caps.data?.operations.images?.reason || "This harness cannot receive these images."} {images.items.map(i=><Button small key={i.key} onClick={()=>images.remove(i.key)}>Remove {i.name}</Button>)}</Alert> : null}
        </> : null}
        {prefilled === undefined || draftConfig ? null : (
          <Hint className="-mt-1.5">model prefilled from the {connectionName(connection)} pin (Settings › Connections)</Hint>
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

/**
 * The harness a form launches on and its execution config: the chosen harness (or the project's only binding),
 * what it can do there, the config draft, and what blocks saving it.
 */
function useHarnessConfig(
  existing: Task | Schedule | undefined,
  projectId: string,
  defaultConnection: number | null,
) {
  const api = useApi();
  const projects = useProjects();
  const harnesses = useHarnesses();
  const selectedProject = projects.data?.projects.find((project) => String(project.id) === projectId);
  const [chosen, setChosen] = useState(existing?.connectionId ?? 0);
  // Bindings on replaced or disconnected connections never pick the destination.
  const bindings = (selectedProject?.bindings ?? []).filter((b) => b.connectionActive && !harnesses.unconfigured(b.connectionId));
  // A default that is no longer an active, connected connection is none.
  const fallbackDefault = defaultConnection !== null && harnesses.active(defaultConnection) && !harnesses.unconfigured(defaultConnection) ? defaultConnection : null;
  // Not chosen: the default connection, or (for a project not available there) the one the server would pick. The
  // default never narrows the project list; only an explicit choice does.
  const implicit = existing
    ? 0
    : selectedProject === undefined
      ? (fallbackDefault ?? 0)
      : newTaskConnection(bindings, fallbackDefault, (id) => harnesses.connection(id)?.machineId ?? Number.MAX_SAFE_INTEGER);
  const connection = chosen || implicit || (bindings.length === 1 ? bindings[0]?.connectionId ?? 0 : 0);
  // The project is not on the default connection: the form says where the task goes instead.
  const fellBack = chosen === 0 && fallbackDefault !== null && selectedProject !== undefined && connection !== fallbackDefault ? fallbackDefault : null;
  // An explicitly chosen harness narrows the picker to projects it can run.
  const pickable = useMemo(() => projectsForConnection(projects.data?.projects ?? [], chosen, projectId), [projects.data, chosen, projectId]);
  const bound = bindings.some(b => b.connectionId === connection && b.status === "available");
  const caps = useQuery({
    queryKey: ["capabilities", connection, projectId, bound],
    enabled: connection > 0 && bound,
    queryFn: () => api.capabilities(connection, Number(projectId)),
    // What the harness can do changes rarely; the form reads it once, not on a poll.
    staleTime: 60_000,
    retry: false,
  });
  // Until the harness answers, nothing is ruled out: the server checks the launch.
  const operation = (id: OperationId) => caps.data?.operations[id]?.available ?? true;
  const [draftConfig, setDraftConfig] = useState<ExecutionConfig | null>(null);
  // A new task starts with the connection's pinned model; it follows the harness until the user edits the config.
  const prefilled = existing ? undefined : prefillConfig(harnesses.connection(connection));
  const originalConfig = existing?.executionConfig ?? prefilled;
  const baseConfig = originalConfig ? { ...originalConfig, options: originalConfig.options ?? [] } : harnessDefaults;
  // New-task prefill carries model and options only; mode and permission start at the harness default.
  const initialConfig = existing ? baseConfig : { ...baseConfig, mode: "", permission: "" };
  const effectiveConfig = draftConfig ?? initialConfig;
  const changedHarness = existing !== undefined && connection !== existing.connectionId;
  const unverifiedConfig = changedHarness && caps.data === undefined &&
    (effectiveConfig.model || effectiveConfig.mode || effectiveConfig.permission || effectiveConfig.options.length > 0);
  function chooseHarness(id: number) {
    if (existing || draftConfig) setDraftConfig(effectiveConfig);
    setChosen(id);
  }
  return {
    harnesses,
    selectedProject,
    bindings,
    chosen,
    connection,
    pickable,
    bound,
    caps,
    acceptsImages: operation("images"),
    prefilled,
    draftConfig,
    setDraftConfig,
    effectiveConfig,
    issues: incompatibleSettings(effectiveConfig, caps.data),
    unverifiedConfig,
    chooseHarness,
    fellBack,
  };
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

/** The picker's empty state for a chosen harness with no usable project. */
function NoHarnessProjects({ connectionId }: { connectionId: number }) {
  const harnesses = useHarnesses();
  const name = harnesses.name(connectionId);
  const settings = <TextLink to="/settings/connections" variant="rest">Settings</TextLink>;
  if (harnesses.connection(connectionId)?.configured === false) {
    return <>{name} is not connected. Connect it in {settings}.</>;
  }
  return <>No {name} projects yet. Open a Git repository in {name}, then Sync now in {settings}.</>;
}

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
