import { useState } from "react";
import { useChains, useProject, useTasks } from "../api/queries.ts";
import { TASK_STATES } from "../api/types.ts";
import { ChainList } from "../components/ChainList.tsx";
import { taskStateLabel } from "../components/StateBadge.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import { TaskList } from "../components/TaskList.tsx";
import { Badge, Button, PageHeader, QueryState, Select } from "../components/ui.tsx";
import { pick } from "../lib/format.ts";
import { useIdParam } from "../lib/params.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function ProjectDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <ProjectDetail id={id} />;
}

function ProjectDetail({ id }: { id: number }) {
  const project = useProject(id);
  const [stateFilter, setStateFilter] = useState("");
  const state = pick(TASK_STATES, stateFilter);
  const tasks = useTasks({ project: id, ...(state === undefined ? {} : { state: [state] }) });
  const chains = useChains();
  const [creating, setCreating] = useState(false);
  const projectChains = (chains.data?.chains ?? []).filter((c) => c.steps.some((s) => s.projectId === id));

  return (
    <QueryState isPending={project.isPending} error={project.error}>
      {project.data === undefined ? null : (
        <>
          <PageHeader
            title={project.data.title}
            subtitle={
              <span className="flex flex-wrap items-center gap-2">
                <span className="font-mono">{project.data.workspaceRoot}</span>
                <Badge tone={project.data.source === "app" ? "blue" : "gray"}>{project.data.source}</Badge>
                {project.data.archivedAt === null ? null : <Badge tone="amber">archived</Badge>}
                <span className="font-mono text-xs">{project.data.t3ProjectId}</span>
              </span>
            }
            actions={
              <Button variant="primary" onClick={() => setCreating(true)}>
                New task
              </Button>
            }
          />
          <div className="flex flex-col gap-6">
            <section>
              <div className="mb-2 flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold text-neutral-600 dark:text-neutral-400">Tasks</h2>
                <Select value={stateFilter} onChange={(e) => setStateFilter(e.target.value)}>
                  <option value="">All states</option>
                  {TASK_STATES.map((s) => (
                    <option key={s} value={s}>
                      {taskStateLabel(s)}
                    </option>
                  ))}
                </Select>
              </div>
              <QueryState isPending={tasks.isPending} error={tasks.error}>
                <TaskList tasks={tasks.data?.tasks ?? []} />
              </QueryState>
            </section>
            <section>
              <h2 className="mb-2 text-sm font-semibold text-neutral-600 dark:text-neutral-400">Chains with steps here</h2>
              <QueryState isPending={chains.isPending} error={chains.error}>
                <ChainList chains={projectChains} />
              </QueryState>
            </section>
          </div>
          {creating ? <TaskFormModal defaultProjectId={id} onClose={() => setCreating(false)} /> : null}
        </>
      )}
    </QueryState>
  );
}
