import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent, type KeyboardEvent } from "react";
import { api } from "../api/client.ts";
import { usePreferences, useInvalidateWork, useProjects, useTemplates } from "../api/queries.ts";
import {
  INTERACTION_MODES,
  type CreateTaskRequest,
  type InteractionMode,
  type ModelDefaults,
  type Preferences,
  type Task,
  type UpdateTaskRequest,
  type Workspace,
} from "../api/types.ts";
import { parseTags } from "../lib/format.ts";
import { isFinal, isFullyEditable } from "../lib/tasks.ts";
import { ModelFields, type ModelConfig } from "./ModelFields.tsx";
import { TaskPicker, useLinkCandidates } from "./Sequence.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Alert, Button, Chip, Drawer, ErrorText, Field, Input, Muted, Segmented, Select, Textarea } from "./ui.tsx";

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
  /** Runs-after predecessor id ("" none). */
  afterTaskId: string;
  /** Follower id ("" none). */
  nextTaskId: string;
}

type DefaultsSource = "pinned" | "last used";

/** New-task model prefill: pinned default, else the last created task's config. */
function pickDefaults(p: Preferences | undefined): { defaults: ModelDefaults; source: DefaultsSource } | null {
  if (p?.pinned != null) return { defaults: p.pinned, source: "pinned" };
  if (p?.lastUsed != null) return { defaults: p.lastUsed, source: "last used" };
  return null;
}

function initialState(task: Task | undefined, defaultProjectId: number | undefined, defaults: ModelDefaults | undefined): FormState {
  if (task === undefined) {
    return {
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
      afterTaskId: "",
      nextTaskId: "",
    };
  }
  return {
    projectId: String(task.projectId),
    title: task.title,
    prompt: task.prompt,
    config: {
      providerInstanceId: task.providerInstanceId,
      model: task.model,
      modelOptions: task.modelOptions,
      runtimeMode: task.runtimeMode,
    },
    interactionMode: task.interactionMode,
    workspaceType: task.workspace.type,
    baseRef: task.workspace.type === "worktree" ? task.workspace.baseRef : "origin/main",
    branch: task.workspace.type === "worktree" ? (task.workspace.branch ?? "") : "",
    tags: task.tags.join(", "),
    notes: task.notes,
    afterTaskId: task.afterTaskId === null ? "" : String(task.afterTaskId),
    nextTaskId: task.nextTaskId === null ? "" : String(task.nextTaskId),
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

interface TaskFormProps {
  task?: Task;
  defaultProjectId?: number;
  onClose: () => void;
  onSaved?: (task: Task) => void;
}

/** Create (no `task`) or edit (`task`) form in a drawer. A new task waits for preferences (usually cached) to prefill its model config. */
export function TaskFormModal(props: TaskFormProps) {
  const prefs = usePreferences();
  if (props.task !== undefined) return <TaskFormDrawer {...props} prefill={null} />;
  if (prefs.isPending) {
    return (
      <Drawer title="New task" onClose={props.onClose} wide>
        <Muted>Loading defaults…</Muted>
      </Drawer>
    );
  }
  return <TaskFormDrawer {...props} prefill={pickDefaults(prefs.data)} />;
}

function TaskFormDrawer({
  task,
  defaultProjectId,
  onClose,
  onSaved,
  prefill,
}: TaskFormProps & { prefill: { defaults: ModelDefaults; source: DefaultsSource } | null }) {
  const [s, setS] = useState(() => initialState(task, defaultProjectId, prefill?.defaults));
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const projects = useProjects();
  const templates = useTemplates();
  const invalidate = useInvalidateWork();
  const full = task === undefined || isFullyEditable(task);
  const candidates = useLinkCandidates(task);
  // Runs after: new or backlog tasks. Then run: any task that can still gate (not settled/cancelled).
  const canLinkBefore = task === undefined || task.state === "backlog";
  const canLinkAfter = task !== undefined && !isFinal(task);
  const after = s.afterTaskId === "" ? null : Number(s.afterTaskId);
  // A new task linked to run after another is saved waiting; it starts when that one settles.
  const queued = task === undefined && after !== null;

  const save = useMutation({
    mutationFn: (start: boolean): Promise<Task> => {
      const tags = parseTags(s.tags);
      if (task !== undefined) {
        const body: UpdateTaskRequest = full
          ? {
              projectId: Number(s.projectId),
              title: s.title.trim(),
              prompt: s.prompt,
              providerInstanceId: s.config.providerInstanceId,
              model: s.config.model,
              modelOptions: s.config.modelOptions,
              runtimeMode: s.config.runtimeMode,
              interactionMode: s.interactionMode,
              workspace: toWorkspace(s),
              tags,
              notes: s.notes,
            }
          : { title: s.title.trim(), tags, notes: s.notes };
        if (canLinkBefore && after !== task.afterTaskId) body.afterTaskId = after;
        return api.updateTask(task.id, body).then((saved) => relinkNext(task, saved));
      }
      const body: CreateTaskRequest = {
        projectId: Number(s.projectId),
        title: s.title.trim(),
        prompt: s.prompt,
        providerInstanceId: s.config.providerInstanceId,
        model: s.config.model,
        modelOptions: s.config.modelOptions,
        runtimeMode: s.config.runtimeMode,
        interactionMode: s.interactionMode,
        workspace: toWorkspace(s),
        tags,
        notes: s.notes,
        ...(after === null ? { start } : { afterTaskId: after }),
      };
      return api.createTask(body);
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
  ].filter((m) => m !== false);

  const submit = (start: boolean) => (e?: FormEvent) => {
    e?.preventDefault();
    if (missing.length === 0) save.mutate(start);
  };

  const applyTemplate = (id: string) => {
    const t = templates.data?.templates.find((x) => String(x.id) === id);
    if (t === undefined) return;
    set({
      prompt: t.prompt === "" ? s.prompt : t.prompt,
      interactionMode: t.interactionMode,
      config: {
        providerInstanceId: t.providerInstanceId,
        model: t.model,
        modelOptions: t.modelOptions,
        runtimeMode: t.runtimeMode,
      },
    });
  };

  const title = task === undefined ? "New task" : `Edit task #${task.id}`;
  const blocked = missing.length > 0 || save.isPending;

  const onKeyDown = (e: KeyboardEvent<HTMLFormElement>) => {
    if (e.key !== "Enter" || !(e.ctrlKey || e.metaKey)) return;
    e.preventDefault();
    if (!save.isPending) submit(!queued)();
  };

  const primaryLabel = task !== undefined ? "Save" : queued ? `Save · runs after #${after}` : "Save & start";

  const footer = (
    <>
      {task === undefined && !queued ? (
        <Button busy={save.isPending && save.variables === false} disabled={blocked} onClick={() => submit(false)()}>
          <span className="md:hidden">Backlog</span>
          <span className="hidden md:inline">Save to backlog</span>
        </Button>
      ) : null}
      <span className="min-w-0 flex-1 text-right font-mono text-[11px] text-muted max-md:order-first max-md:basis-full max-md:text-left empty:hidden">
        {missing.length > 0 ? `required: ${missing.join(", ")}` : null}
      </span>
      <Button
        variant="primary"
        busy={save.isPending && (task !== undefined || queued || save.variables === true)}
        disabled={blocked}
        onClick={() => submit(!queued)()}
      >
        {primaryLabel}
        <kbd className="hidden border border-current/40 px-1 font-mono text-[10.5px] leading-4 md:inline">Ctrl ↵</kbd>
      </Button>
    </>
  );

  return (
    <Drawer title={title} onClose={onClose} wide footer={footer}>
      <form onSubmit={submit(false)} onKeyDown={onKeyDown} className="flex flex-col gap-3.5">
        {task === undefined || full ? null : (
          <Alert tone="plain">
            Task is <TaskStateBadge state={task.state} />; only title, tags and notes can change.
          </Alert>
        )}
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="Project">
            {(id) => (
              <Select id={id} disabled={!full} value={s.projectId} onChange={(e) => set({ projectId: e.target.value })}>
                <option value="">Select project…</option>
                {(projects.data?.projects ?? []).map((p) => (
                  <option key={p.id} value={String(p.id)}>
                    {p.title}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          {full ? (
            <Field label="Template">
              {(id) => (
                <Select id={id} value="" onChange={(e) => applyTemplate(e.target.value)}>
                  <option value="">Prefill from template…</option>
                  {(templates.data?.templates ?? []).map((t) => (
                    <option key={t.id} value={String(t.id)}>
                      {t.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          ) : null}
        </div>
        <Field label="Title">
          {(id) => <Input id={id} data-autofocus value={s.title} onChange={(e) => set({ title: e.target.value })} />}
        </Field>
        <Field label="Prompt">
          {(id) => (
            <Textarea
              id={id}
              rows={8}
              disabled={!full}
              value={s.prompt}
              onChange={(e) => set({ prompt: e.target.value })}
            />
          )}
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
              <Field label="Branch" hint="Empty = T3 picks a name.">
                {(id) => <Input id={id} value={s.branch} onChange={(e) => set({ branch: e.target.value })} />}
              </Field>
            </>
          ) : null}
        </fieldset>
        {prefill === null ? null : (
          <p className="-mt-1.5 font-mono text-[11px] text-muted">
            {prefill.source === "pinned"
              ? "model and mode prefilled from your pinned default (Settings)"
              : "model and mode prefilled from your last task · pin a default in Settings"}
          </p>
        )}
        {canLinkBefore || canLinkAfter ? (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {canLinkBefore ? (
              <Field label="Runs after" hint={after === null ? "Optional. Starts when that task settles in T3." : "Saved waiting; starts when that task settles in T3."}>
                {(id) => (
                  <TaskPicker
                    id={id}
                    value={s.afterTaskId}
                    onChange={(afterTaskId) => set({ afterTaskId })}
                    options={candidates.before}
                    none="Nothing (start on its own)"
                  />
                )}
              </Field>
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
          </div>
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
          {(id) => <Textarea id={id} rows={3} value={s.notes} onChange={(e) => set({ notes: e.target.value })} />}
        </Field>
        <ErrorText error={save.error} />
      </form>
    </Drawer>
  );
}
