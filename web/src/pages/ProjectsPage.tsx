import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { keys, useProjects } from "../api/queries.ts";
import type { Project } from "../api/types.ts";
import { relativeTime } from "../lib/format.ts";
import { Badge, Button, Card, ErrorText, Field, Input, PageHeader, QueryState } from "../components/ui.tsx";

function CreateProject() {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const [root, setRoot] = useState("");
  const create = useMutation({
    mutationFn: () => api.createProject({ title: title.trim(), workspaceRoot: root.trim() }),
    onSuccess: async () => {
      setTitle("");
      setRoot("");
      await qc.invalidateQueries({ queryKey: keys.projects });
    },
  });
  const valid = title.trim() !== "" && root.trim() !== "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (valid) create.mutate();
  };

  return (
    <Card title="New project">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_2fr_auto] sm:items-end">
          <Field label="Title">{(id) => <Input id={id} value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
          <Field label="Workspace path">
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
          <Button type="submit" variant="primary" busy={create.isPending} disabled={!valid}>
            Create
          </Button>
        </div>
        <p className="text-xs text-neutral-500">
          Links the T3 project at this path if one exists, otherwise creates it in T3.
        </p>
        <ErrorText error={create.error} />
      </form>
    </Card>
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

export function ProjectsPage() {
  const [showArchived, setShowArchived] = useState(false);
  const projects = useProjects(showArchived ? "all" : "false");
  const list = projects.data?.projects ?? [];

  return (
    <>
      <PageHeader
        title="Projects"
        actions={
          <label className="flex items-center gap-2 text-sm text-neutral-600 dark:text-neutral-400">
            <input type="checkbox" checked={showArchived} onChange={(e) => setShowArchived(e.target.checked)} />
            Show archived
          </label>
        }
      />
      <div className="flex flex-col gap-5">
        <CreateProject />
        <QueryState isPending={projects.isPending} error={projects.error} empty={list.length === 0} emptyText="No projects yet.">
          <ul className="divide-y divide-neutral-200 rounded-lg border border-neutral-200 bg-white dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
            {list.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                <div className="min-w-0 flex-1 basis-60">
                  <Link to={`/projects/${p.id}`} className="font-medium hover:underline">
                    {p.title}
                  </Link>
                  <div className="truncate font-mono text-xs text-neutral-500" title={p.workspaceRoot}>
                    {p.workspaceRoot}
                  </div>
                </div>
                <Badge tone={p.source === "app" ? "blue" : "gray"}>{p.source === "app" ? "created here" : "from T3"}</Badge>
                {p.archivedAt === null ? null : <Badge tone="amber">archived</Badge>}
                <span className="text-xs text-neutral-500">{relativeTime(p.createdAt)}</span>
                <ArchiveButton project={p} />
              </li>
            ))}
          </ul>
        </QueryState>
      </div>
    </>
  );
}
