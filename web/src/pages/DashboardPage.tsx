import { useState } from "react";
import { useSearchParams } from "react-router";
import { useT3Status, useTasks } from "../api/queries.ts";
import type { Task, TaskState } from "../api/types.ts";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import { TaskList } from "../components/TaskList.tsx";
import { Button, ExternalButton, PageHeader, QueryState } from "../components/ui.tsx";

const GROUPS: { title: string; states: TaskState[]; hideEmpty?: boolean }[] = [
  { title: "Needs input", states: ["needs_input"] },
  { title: "Running", states: ["launching", "running"] },
  { title: "Completed (awaiting settle in T3)", states: ["completed"] },
  { title: "Failed / interrupted", states: ["failed", "interrupted"], hideEmpty: true },
  { title: "Backlog", states: ["backlog"] },
];

const ALL_STATES = GROUPS.flatMap((g) => g.states);

function NeedsInputBanner({ tasks }: { tasks: Task[] }) {
  const t3 = useT3Status();
  const first = tasks[0];
  if (first === undefined) return null;
  const href = tasks.length === 1 ? first.t3WebUrl : (t3.data?.publicUrl ?? null);
  return (
    <div className="mb-5 flex flex-wrap items-center gap-3 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
      <span className="rounded-full bg-amber-500 px-2 py-0.5 text-sm font-semibold text-white">{tasks.length}</span>
      <span className="flex-1 text-sm font-medium">
        {tasks.length === 1 ? `"${first.title}" needs input` : `${tasks.length} tasks need input`}
      </span>
      {href === null ? null : (
        <ExternalButton href={href} small>
          Open in T3
        </ExternalButton>
      )}
    </div>
  );
}

export function DashboardPage() {
  const [params, setParams] = useSearchParams();
  const project = params.get("project") ?? "";
  const [creating, setCreating] = useState(false);
  const tasks = useTasks({ state: ALL_STATES, ...(project === "" ? {} : { project: Number(project) }) });
  const all = tasks.data?.tasks ?? [];
  const byState = (states: TaskState[]) => all.filter((t) => states.includes(t.state));

  return (
    <>
      <PageHeader
        title="Dashboard"
        actions={
          <>
            <ProjectSelect value={project} onChange={(v) => setParams(v === "" ? {} : { project: v })} />
            <Button variant="primary" onClick={() => setCreating(true)}>
              New task
            </Button>
          </>
        }
      />
      <QueryState isPending={tasks.isPending} error={tasks.error}>
        <NeedsInputBanner tasks={byState(["needs_input"])} />
        <div className="flex flex-col gap-6">
          {GROUPS.map((g) => {
            const list = byState(g.states);
            if (g.hideEmpty === true && list.length === 0) return null;
            return (
              <section key={g.title}>
                <h2 className="mb-2 text-sm font-semibold text-neutral-600 dark:text-neutral-400">
                  {g.title} <span className="font-normal text-neutral-400">({list.length})</span>
                </h2>
                <TaskList tasks={list} emptyText="None." />
              </section>
            );
          })}
        </div>
      </QueryState>
      {creating ? (
        <TaskFormModal
          onClose={() => setCreating(false)}
          {...(project === "" ? {} : { defaultProjectId: Number(project) })}
        />
      ) : null}
    </>
  );
}
