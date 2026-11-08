import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { api } from "../api/client.ts";
import { keys, useTemplates } from "../api/queries.ts";
import { INTERACTION_MODES, type CreateTemplateRequest, type InteractionMode, type Template } from "../api/types.ts";
import { ModelFields, type ModelConfig } from "../components/ModelFields.tsx";
import { Button, Chip, Drawer, ErrorText, Field, Input, PageHeader, Panel, QueryState, Select, Textarea } from "../components/ui.tsx";
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
        meta={`${list.length} presets · prompt + model for the task form`}
        actions={
          <Button variant="primary" onClick={() => setEditing({ mode: "create" })}>
            New template
          </Button>
        }
      />
      <QueryState
        isPending={templates.isPending}
        error={templates.error}
        empty={list.length === 0}
        emptyText="No templates yet. Create one to prefill prompt and model on new tasks."
      >
        <Panel title="Templates" meta={list.length} flush>
          <ul>
            {list.map((t) => (
              <TemplateRow key={t.id} template={t} onEdit={() => setEditing({ mode: "edit", template: t })} />
            ))}
          </ul>
        </Panel>
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
    <li className="border-b border-line px-3 py-2.5 last:border-b-0">
      <div className="flex flex-col gap-2 md:flex-row md:items-center md:gap-3">
        <div className="min-w-0 flex-1">
          <button type="button" onClick={onEdit} className="text-left text-[13.5px] font-medium hover:underline">
            {t.name}
          </button>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 font-mono text-[11.5px] text-muted">
            <span>{t.providerInstanceId ?? "T3 default"}</span>
            <span className="min-w-0 break-all">{modelText(t.model, t.modelOptions)}</span>
            <span>{t.runtimeMode}</span>
            {t.interactionMode === "plan" ? <Chip>plan</Chip> : null}
          </div>
          {t.prompt === "" ? null : <p className="mt-1 truncate font-mono text-[11.5px] text-muted">{t.prompt}</p>}
        </div>
        <div className="flex shrink-0 gap-2">
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
      </div>
      {remove.error === null ? null : (
        <div className="mt-2">
          <ErrorText error={remove.error} />
        </div>
      )}
    </li>
  );
}

function TemplateFormModal({ template, onClose }: { template?: Template; onClose: () => void }) {
  const qc = useQueryClient();
  const formId = useId();
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
    <Drawer
      title={template === undefined ? "New template" : "Edit template"}
      onClose={onClose}
      wide
      footer={
        <>
          <Button onClick={onClose} className="ml-auto">
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" busy={save.isPending} disabled={name.trim() === ""}>
            Save
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} data-autofocus />}</Field>
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
      </form>
    </Drawer>
  );
}
