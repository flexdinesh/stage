import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api } from "../api/client.ts";
import { useInvalidateWork, useProjects, useTemplates } from "../api/queries.ts";
import {
  INTERACTION_MODES,
  type CreateTaskRequest,
  type InteractionMode,
  type Task,
  type UpdateTaskRequest,
  type Workspace,
} from "../api/types.ts";
import { parseTags, pick } from "../lib/format.ts";
import { isFullyEditable } from "../lib/tasks.ts";
import { ModelFields, type ModelConfig } from "./ModelFields.tsx";
import { Badge, Button, ErrorText, Field, Input, Modal, Select, Textarea } from "./ui.tsx";

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
}

function initialState(task: Task | undefined, defaultProjectId: number | undefined): FormState {
  if (task === undefined) {
    return {
      projectId: defaultProjectId === undefined ? "" : String(defaultProjectId),
      title: "",
      prompt: "",
      config: { providerInstanceId: null, model: null, modelOptions: [], runtimeMode: "full-access" },
      interactionMode: "default",
      workspaceType: "root",
      baseRef: "origin/main",
      branch: "",
      tags: "",
      notes: "",
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
  };
}

function toWorkspace(s: FormState): Workspace {
  if (s.workspaceType === "root") return { type: "root" };
  const branch = s.branch.trim();
  return { type: "worktree", baseRef: s.baseRef.trim(), branch: branch === "" ? null : branch };
}

/** Create (no `task`) or edit (`task`) form in a modal. `chainId` appends the new task to that chain. */
export function TaskFormModal({
  task,
  defaultProjectId,
  chainId,
  onClose,
  onSaved,
}: {
  task?: Task;
  defaultProjectId?: number;
  chainId?: number;
  onClose: () => void;
  onSaved?: (task: Task) => void;
}) {
  const [s, setS] = useState(() => initialState(task, defaultProjectId));
  const set = (patch: Partial<FormState>) => setS((prev) => ({ ...prev, ...patch }));
  const projects = useProjects();
  const templates = useTemplates();
  const invalidate = useInvalidateWork();
  const full = task === undefined || isFullyEditable(task);

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
        return api.updateTask(task.id, body);
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
        ...(chainId === undefined ? { start } : { chainId }),
      };
      return api.createTask(body);
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

  const title = task === undefined ? (chainId === undefined ? "New task" : "New chain step") : `Edit task #${task.id}`;

  return (
    <Modal title={title} onClose={onClose} wide>
      <form onSubmit={submit(false)} className="flex flex-col gap-4">
        {full ? null : (
          <p className="text-xs text-neutral-500">
            Task is <Badge>{task?.state ?? ""}</Badge>; only title, tags and notes can change.
          </p>
        )}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
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
          {(id) => <Input id={id} value={s.title} onChange={(e) => set({ title: e.target.value })} autoFocus />}
        </Field>
        <Field label="Prompt">
          {(id) => (
            <Textarea
              id={id}
              rows={10}
              disabled={!full}
              value={s.prompt}
              onChange={(e) => set({ prompt: e.target.value })}
            />
          )}
        </Field>
        <ModelFields value={s.config} onChange={(config) => set({ config })} disabled={!full} />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Interaction mode">
            {(id) => (
              <Select
                id={id}
                disabled={!full}
                value={s.interactionMode}
                onChange={(e) => set({ interactionMode: pick(INTERACTION_MODES, e.target.value) ?? s.interactionMode })}
              >
                {INTERACTION_MODES.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Workspace">
            {(id) => (
              <Select
                id={id}
                disabled={!full}
                value={s.workspaceType}
                onChange={(e) => set({ workspaceType: e.target.value === "worktree" ? "worktree" : "root" })}
              >
                <option value="root">Project root</option>
                <option value="worktree">New worktree</option>
              </Select>
            )}
          </Field>
          {s.workspaceType === "worktree" ? (
            <>
              <Field label="Base ref">
                {(id) => (
                  <Input id={id} disabled={!full} value={s.baseRef} onChange={(e) => set({ baseRef: e.target.value })} />
                )}
              </Field>
              <Field label="Branch" hint="Empty = T3 picks a name.">
                {(id) => (
                  <Input id={id} disabled={!full} value={s.branch} onChange={(e) => set({ branch: e.target.value })} />
                )}
              </Field>
            </>
          ) : null}
        </div>
        <Field
          label="Tags"
          hint={
            parseTags(s.tags).length === 0 ? (
              "Comma separated."
            ) : (
              <span className="flex flex-wrap gap-1">
                {parseTags(s.tags).map((t) => (
                  <Badge key={t} tone="violet">
                    {t}
                  </Badge>
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
        <div className="flex flex-wrap items-center justify-end gap-2">
          {missing.length > 0 ? <span className="mr-auto text-xs text-neutral-500">Required: {missing.join(", ")}</span> : null}
          <Button onClick={onClose}>Cancel</Button>
          {task !== undefined ? (
            <Button type="submit" variant="primary" busy={save.isPending} disabled={missing.length > 0}>
              Save
            </Button>
          ) : chainId !== undefined ? (
            <Button type="submit" variant="primary" busy={save.isPending} disabled={missing.length > 0}>
              Add to chain
            </Button>
          ) : (
            <>
              <Button type="submit" busy={save.isPending && save.variables === false} disabled={missing.length > 0 || save.isPending}>
                Save to backlog
              </Button>
              <Button
                variant="primary"
                busy={save.isPending && save.variables === true}
                disabled={missing.length > 0 || save.isPending}
                onClick={() => submit(true)()}
              >
                Save &amp; start
              </Button>
            </>
          )}
        </div>
      </form>
    </Modal>
  );
}
