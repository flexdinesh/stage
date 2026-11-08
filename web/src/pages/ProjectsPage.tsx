import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { keys, useProjects, useTasks } from "../api/queries.ts";
import type { Project, Task } from "../api/types.ts";
import { IconPlus } from "../components/icons.tsx";
import {
  Button,
  Chip,
  Drawer,
  ErrorText,
  Field,
  Input,
  PageHeader,
  Panel,
  QueryState,
  Segmented,
} from "../components/ui.tsx";

const FORM_ID = "new-project-form";

function CreateProjectDrawer({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const [root, setRoot] = useState("");
  const create = useMutation({
    mutationFn: () => api.createProject({ title: title.trim(), workspaceRoot: root.trim() }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.projects });
      onClose();
    },
  });
  const valid = title.trim() !== "" && root.trim() !== "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (valid) create.mutate();
  };

  return (
    <Drawer
      title="New project"
      onClose={onClose}
      footer={
        <>
          <Button type="submit" form={FORM_ID} variant="primary" busy={create.isPending} disabled={!valid}>
            Create project
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </>
      }
    >
      <form id={FORM_ID} onSubmit={submit} className="flex flex-col gap-3">
        <Field label="Title">
          {(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}
        </Field>
        <Field label="Workspace path" hint="Links the T3 project at this path if one exists, otherwise creates it in T3.">
          {(id) => (
            <Input
              id={id}
              value={root}
              onChange={(e) => setRoot(e.target.value)}
              placeholder="/home/dee/workspace/my-app"
              className="font-mono"
            />
          )}
        </Field>
        <ErrorText error={create.error} />
      </form>
    </Drawer>
  );
}

function ArchiveButton({ project }: { project: Project }) {
  const qc = useQueryClient();
  const archived = project.archivedAt !== null;
  const toggle = useMutation({
    mutationFn: () => api.updateProject(project.id, { archived: !archived }),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.projects }),
  });
  return (
    <Button small variant="ghost" busy={toggle.isPending} onClick={() => toggle.mutate()} title={errorTitle(toggle.error)}>
      {archived ? "Unarchive" : "Archive"}
    </Button>
  );
}

const errorTitle = (e: Error | null) => (e === null ? undefined : e.message);

type Counts = { total: number; active: number };

function countByProject(tasks: Task[]): Map<number, Counts> {
  const out = new Map<number, Counts>();
  for (const t of tasks) {
    const c = out.get(t.projectId) ?? { total: 0, active: 0 };
    c.total += 1;
    if (t.state === "launching" || t.state === "running" || t.state === "needs_input") c.active += 1;
    out.set(t.projectId, c);
  }
  return out;
}

const ARCHIVED_OPTIONS = [
  { value: "active", label: "active" },
  { value: "all", label: "incl. archived" },
] as const;

export function ProjectsPage() {
  const [filter, setFilter] = useState<"active" | "all">("active");
  const [creating, setCreating] = useState(false);
  const projects = useProjects(filter === "all" ? "all" : "false");
  const tasks = useTasks();
  const list = projects.data?.projects ?? [];
  const counts = tasks.data === undefined ? null : countByProject(tasks.data.tasks);

  return (
    <>
      <PageHeader
        title="Projects"
        meta={`${projects.data === undefined ? "…" : list.length} projects · archived ${filter === "all" ? "shown" : "hidden"}`}
        actions={
          <>
            <Segmented small label="Archived filter" options={ARCHIVED_OPTIONS} value={filter} onChange={setFilter} />
            <Button variant="primary" onClick={() => setCreating(true)}>
              <IconPlus /> New project
            </Button>
          </>
        }
      />
      <QueryState
        isPending={projects.isPending}
        error={projects.error}
        empty={list.length === 0}
        emptyText="No projects yet. Create one from a workspace path."
      >
        <Panel flush title={filter === "all" ? "All projects" : "Active projects"} meta={list.length}>
          <ul>
            {list.map((p) => {
              const c = counts?.get(p.id) ?? { total: 0, active: 0 };
              return (
                <li
                  key={p.id}
                  className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-b border-line px-3 py-2 last:border-b-0 md:grid-cols-[minmax(0,1fr)_auto_128px_96px]"
                >
                  <div className="min-w-0">
                    <Link to={`/projects/${p.id}`} className="block truncate text-[13.5px] font-medium hover:underline">
                      {p.title}
                    </Link>
                    <div className="truncate font-mono text-[11.5px] text-muted" title={p.workspaceRoot}>
                      {p.workspaceRoot}
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 justify-self-end md:justify-self-start">
                    {p.archivedAt === null ? null : <Chip tone="idle">archived</Chip>}
                    <Chip title={p.source === "app" ? "Created here" : "Imported from T3"}>{p.source === "app" ? "app" : "t3"}</Chip>
                  </div>
                  <span className="font-mono text-[11.5px] text-muted tabular">
                    {counts === null ? null : (
                      <>
                        {c.total} {c.total === 1 ? "task" : "tasks"}
                        {c.active === 0 ? null : <span className="text-fg"> · {c.active} active</span>}
                      </>
                    )}
                  </span>
                  <div className="justify-self-end">
                    <ArchiveButton project={p} />
                  </div>
                </li>
              );
            })}
          </ul>
        </Panel>
      </QueryState>
      {creating ? <CreateProjectDrawer onClose={() => setCreating(false)} /> : null}
    </>
  );
}
