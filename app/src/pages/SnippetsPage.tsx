import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useApi } from "../platform/context.tsx";
import { keys, useProjects, useSnippets } from "../api/queries.ts";
import type { CreateSnippetRequest, Project, Snippet } from "../api/types.ts";
import { CopyButton } from "../components/CopyButton.tsx";
import { IconChevronDown } from "../components/icons.tsx";
import { ProjectCombobox } from "../components/ProjectSelect.tsx";
import { Button, Drawer, ErrorText, Field, Input, PageHeader, Panel, QueryState, Textarea, cx } from "../components/ui.tsx";

type Editing = { mode: "create" } | { mode: "edit"; snippet: Snippet };

interface Group {
  key: string;
  title: string;
  snippets: Snippet[];
}

/** Global first, then one group per project in server order (project title). */
function groupByScope(list: readonly Snippet[], projects: ReadonlyMap<number, Project>): Group[] {
  const groups = new Map<string, Group>();
  for (const s of list) {
    const key = s.projectId === null ? "global" : String(s.projectId);
    let g = groups.get(key);
    if (g === undefined) {
      const title = s.projectId === null ? "Global" : (projects.get(s.projectId)?.title ?? `Project ${s.projectId}`);
      g = { key, title, snippets: [] };
      groups.set(key, g);
    }
    g.snippets.push(s);
  }
  return [...groups.values()];
}

export function SnippetsPage() {
  const snippets = useSnippets();
  const projects = useProjects("all");
  const [editing, setEditing] = useState<Editing | null>(null);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set());
  const list = useMemo(() => snippets.data?.snippets ?? [], [snippets.data]);
  const byId = useMemo(() => new Map((projects.data?.projects ?? []).map((p) => [p.id, p])), [projects.data]);
  const groups = useMemo(() => groupByScope(list, byId), [list, byId]);

  const toggle = (id: number) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const setGroup = (g: Group, open: boolean) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      for (const s of g.snippets) {
        if (open) next.add(s.id);
        else next.delete(s.id);
      }
      return next;
    });

  return (
    <>
      <PageHeader
        title="Snippets"
        meta={`${list.length} ${list.length === 1 ? "snippet" : "snippets"} · type / in a prompt to insert`}
        actions={
          <Button variant="primary" onClick={() => setEditing({ mode: "create" })}>
            New snippet
          </Button>
        }
      />
      <QueryState
        isPending={snippets.isPending}
        error={snippets.error}
        empty={list.length === 0}
        emptyText="No snippets yet. Create one, then type / in a task prompt to insert it."
      >
        <div className="flex flex-col gap-3.5">
          {groups.map((g) => {
            const allOpen = g.snippets.every((s) => expanded.has(s.id));
            return (
              <Panel
                key={g.key}
                title={g.title}
                meta={<span className="tabular">{g.snippets.length}</span>}
                flush
                actions={
                  <Button variant="ghost" small onClick={() => setGroup(g, !allOpen)}>
                    {allOpen ? "Collapse all" : "Expand all"}
                  </Button>
                }
              >
                <ul>
                  {g.snippets.map((s) => (
                    <SnippetRow
                      key={s.id}
                      snippet={s}
                      expanded={expanded.has(s.id)}
                      onToggle={() => toggle(s.id)}
                      onEdit={() => setEditing({ mode: "edit", snippet: s })}
                    />
                  ))}
                </ul>
              </Panel>
            );
          })}
        </div>
      </QueryState>
      {editing === null ? null : (
        <SnippetFormDrawer
          {...(editing.mode === "edit" ? { snippet: editing.snippet } : {})}
          projects={projects.data?.projects ?? []}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}

function SnippetRow({
  snippet: s,
  expanded,
  onToggle,
  onEdit,
}: {
  snippet: Snippet;
  expanded: boolean;
  onToggle: () => void;
  onEdit: () => void;
}) {
  const api = useApi();
  const qc = useQueryClient();
  const bodyId = useId();
  const remove = useMutation({
    mutationFn: () => api.deleteSnippet(s.id),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.snippets }),
  });
  return (
    <li className="border-b border-line px-3 py-2.5 last:border-b-0">
      <div className="flex flex-col gap-2 md:flex-row md:items-start md:gap-3">
        <div className="flex min-w-0 flex-1 items-start gap-2">
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={bodyId}
            aria-label={expanded ? `Collapse ${s.name}` : `Expand ${s.name}`}
            onClick={onToggle}
            className="mt-px inline-flex size-5 shrink-0 items-center justify-center text-muted transition-colors duration-150 hover:bg-panel hover:text-fg"
          >
            <span aria-hidden className={cx("inline-flex transition-transform duration-150", !expanded && "-rotate-90")}>
              <IconChevronDown size={12} />
            </span>
          </button>
          <div className="min-w-0 flex-1">
            <button type="button" onClick={onToggle} className="text-left text-body font-medium break-words hover:underline">
              {s.name}
            </button>
            {expanded ? (
              <div id={bodyId} className="mt-1.5 flex flex-col items-start gap-2">
                <pre className="w-full font-mono text-small leading-relaxed break-words whitespace-pre-wrap">{s.body}</pre>
                <CopyButton text={s.body} label="Copy" />
              </div>
            ) : (
              <p
                id={bodyId}
                onClick={onToggle}
                className="mt-0.5 line-clamp-2 cursor-pointer font-mono text-meta break-words whitespace-pre-wrap text-muted"
              >
                {s.body}
              </p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-2 max-md:pl-7">
          <Button small onClick={onEdit}>
            Edit
          </Button>
          <Button
            small
            variant="danger"
            busy={remove.isPending}
            onClick={() => {
              if (window.confirm(`Delete snippet "${s.name}"?`)) remove.mutate();
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

function SnippetFormDrawer({ snippet, projects, onClose }: { snippet?: Snippet; projects: Project[]; onClose: () => void }) {
  const api = useApi();
  const qc = useQueryClient();
  const formId = useId();
  const [name, setName] = useState(snippet?.name ?? "");
  const [body, setBody] = useState(snippet?.body ?? "");
  const [projectId, setProjectId] = useState(snippet?.projectId == null ? "" : String(snippet.projectId));
  // Archived projects are hidden unless this snippet already belongs to one.
  const options = projects.filter((p) => p.archivedAt === null || String(p.id) === projectId);
  const ready = name.trim() !== "" && body.trim() !== "";

  const save = useMutation({
    mutationFn: () => {
      const req: CreateSnippetRequest = { name: name.trim(), body, projectId: projectId === "" ? null : Number(projectId) };
      return snippet === undefined ? api.createSnippet(req) : api.updateSnippet(snippet.id, req);
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.snippets });
      onClose();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready) save.mutate();
  };

  return (
    <Drawer
      title={snippet === undefined ? "New snippet" : "Edit snippet"}
      onClose={onClose}
      wide
      footer={
        <>
          <Button onClick={onClose} className="ml-auto">
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" busy={save.isPending} disabled={!ready}>
            Save
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit} className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="Name" hint="Type / and part of the name in a prompt to insert.">
            {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} data-autofocus />}
          </Field>
          <Field label="Project" hint="Global snippets show in every project.">
            {(id) => (
              <ProjectCombobox id={id} projects={options} value={projectId} onChange={setProjectId} pinned="Global" />
            )}
          </Field>
        </div>
        <Field label="Text">{(id) => <Textarea id={id} rows={12} value={body} onChange={(e) => setBody(e.target.value)} />}</Field>
        <ErrorText error={save.error} />
      </form>
    </Drawer>
  );
}
