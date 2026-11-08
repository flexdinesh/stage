import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api } from "../api/client.ts";
import { keys, useTemplates } from "../api/queries.ts";
import { INTERACTION_MODES, type CreateTemplateRequest, type InteractionMode, type Template } from "../api/types.ts";
import { ModelFields, type ModelConfig } from "../components/ModelFields.tsx";
import { Badge, Button, ErrorText, Field, Input, Modal, PageHeader, QueryState, Select, Textarea } from "../components/ui.tsx";
import { modelText, pick } from "../lib/format.ts";

type Editing = { mode: "create" } | { mode: "edit"; template: Template };

export function TemplatesPage() {
  const templates = useTemplates();
  const [editing, setEditing] = useState<Editing | null>(null);
  const list = templates.data?.templates ?? [];

  return (
    <>
      <PageHeader
        title="Templates"
        subtitle="Reusable prompt + model presets for the task form."
        actions={
          <Button variant="primary" onClick={() => setEditing({ mode: "create" })}>
            New template
          </Button>
        }
      />
      <QueryState isPending={templates.isPending} error={templates.error} empty={list.length === 0} emptyText="No templates yet.">
        <ul className="divide-y divide-neutral-200 rounded-lg border border-neutral-200 bg-white dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
          {list.map((t) => (
            <TemplateRow key={t.id} template={t} onEdit={() => setEditing({ mode: "edit", template: t })} />
          ))}
        </ul>
      </QueryState>
      {editing === null ? null : (
        <TemplateFormModal
          {...(editing.mode === "edit" ? { template: editing.template } : {})}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}

function TemplateRow({ template: t, onEdit }: { template: Template; onEdit: () => void }) {
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => api.deleteTemplate(t.id),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.templates }),
  });
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <div className="min-w-0 flex-1 basis-60">
          <button type="button" onClick={onEdit} className="text-left font-medium hover:underline">
            {t.name}
          </button>
          <div className="flex flex-wrap gap-x-2 text-xs text-neutral-500">
            <span>{t.providerInstanceId ?? "T3 default"}</span>
            <span>{modelText(t.model, t.modelOptions)}</span>
            <span>{t.runtimeMode}</span>
            {t.interactionMode === "plan" ? <Badge tone="violet">plan</Badge> : null}
          </div>
          {t.prompt === "" ? null : <p className="mt-0.5 truncate text-xs text-neutral-500">{t.prompt}</p>}
        </div>
        <Button small onClick={onEdit}>
          Edit
        </Button>
        <Button
          small
          variant="danger"
          busy={remove.isPending}
          onClick={() => {
            if (window.confirm(`Delete template "${t.name}"?`)) remove.mutate();
          }}
        >
          Delete
        </Button>
      </div>
      {remove.error === null ? null : (
        <div className="mt-1.5">
          <ErrorText error={remove.error} />
        </div>
      )}
    </li>
  );
}

function TemplateFormModal({ template, onClose }: { template?: Template; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(template?.name ?? "");
  const [prompt, setPrompt] = useState(template?.prompt ?? "");
  const [interactionMode, setInteractionMode] = useState<InteractionMode>(template?.interactionMode ?? "default");
  const [config, setConfig] = useState<ModelConfig>({
    providerInstanceId: template?.providerInstanceId ?? null,
    model: template?.model ?? null,
    modelOptions: template?.modelOptions ?? [],
    runtimeMode: template?.runtimeMode ?? "full-access",
  });

  const save = useMutation({
    mutationFn: () => {
      const body: CreateTemplateRequest = { name: name.trim(), prompt, interactionMode, ...config };
      return template === undefined ? api.createTemplate(body) : api.updateTemplate(template.id, body);
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.templates });
      onClose();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim() !== "") save.mutate();
  };

  return (
    <Modal title={template === undefined ? "New template" : `Edit template`} onClose={onClose} wide>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}</Field>
        <Field label="Prompt" hint="Optional. Empty keeps the task's own prompt when applied.">
          {(id) => <Textarea id={id} rows={6} value={prompt} onChange={(e) => setPrompt(e.target.value)} />}
        </Field>
        <ModelFields value={config} onChange={setConfig} />
        <Field label="Interaction mode" className="sm:w-1/2">
          {(id) => (
            <Select
              id={id}
              value={interactionMode}
              onChange={(e) => setInteractionMode(pick(INTERACTION_MODES, e.target.value) ?? interactionMode)}
            >
              {INTERACTION_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <ErrorText error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" busy={save.isPending} disabled={name.trim() === ""}>
            Save
          </Button>
        </div>
      </form>
    </Modal>
  );
}
