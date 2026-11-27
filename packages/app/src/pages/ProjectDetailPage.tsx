import { ProjectGroupWorkspaces } from "../components/ProjectGroupWorkspaces.tsx";
import { Navigate, useLocation } from "react-router";
import { GroupSummary } from "../components/GroupWorkspaceDisplay.tsx";
import { useWorkspaceConnections } from "../components/WorkspaceDisplay.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { isNotFound, useProjectGroup, useSchedules, useTasks } from "../api/queries.ts";
import { TASK_STATES, type TaskListQuery } from "../api/types.ts";
import { IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { ScheduleRows } from "../components/ScheduleList.tsx";
import { taskStateLabel } from "../components/StateBadge.tsx";
import { TaskRows } from "../components/TaskList.tsx";
import { Button, PageHeader, Panel, QueryState, Segmented, Combobox } from "../components/ui.tsx";
import { pick } from "../lib/format.ts";
import { useIdParam, useSearchParam } from "../lib/params.ts";
import { NotFoundPage, RecordNotFound } from "./NotFoundPage.tsx";

/** Open: not archived and not in a done or archived tree; Closed: done or archived. */
type Scope = "open" | "closed" | "all";

const SCOPE_VALUES: readonly Scope[] = ["open", "closed", "all"];

const SCOPES: { value: Scope; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "all", label: "All" },
];

export function ProjectDetailPage() {
  const id = useIdParam();
  // Keyed: another project's drafts, filters and drawers never carry over (Back/Forward reuses the route element).
  return id === null ? <NotFoundPage /> : <ProjectDetail key={id} id={id} />;
}

function ProjectDetail({ id }: { id: number }) {
  const project = useProjectGroup(id);
  const projectId = project.data?.id ?? id;
  const location = useLocation();
  const connections = useWorkspaceConnections();
  // In the address, so Back from a task keeps the list as it was.
  const [stateFilter, setStateFilter] = useSearchParam("state", ["", ...TASK_STATES], "");
  const [scope, setScope] = useSearchParam("scope", SCOPE_VALUES, "open");
  const state = pick(TASK_STATES, stateFilter);
  const closed: TaskListQuery["closed"] = scope === "closed" ? "true" : scope === "all" ? "all" : undefined;
  const tasks = useTasks({
    project: projectId,
    ...(state === undefined ? {} : { state: [state] }),
    ...(closed === undefined ? {} : { closed }),
  });
  const newTask = useNewTask();
  const taskCount = tasks.data?.tasks.length;
  const schedules = useSchedules({ project: projectId });
  const scheduleList = schedules.data?.schedules ?? [];

  if (isNotFound(project.error)) {
    return <RecordNotFound title="Project not found" meta={`project ${id}`} text="This project was deleted or does not exist." back={{ to: "/projects", label: "Back to projects" }} />;
  }
  if (projectId !== id) return <Navigate replace to={`/projects/${projectId}${location.search}${location.hash}`} />;
  const startingMode = project.data?.state === "current" ? undefined : "backlog";
  return (
    <QueryState isPending={project.isPending} error={project.error} hasData={project.data !== undefined}>
      {project.data === undefined ? null : (
        <>
          <PageHeader
            title={project.data.title}
            trail={[{ label: "Projects", to: "/projects" }, { label: `project ${project.data.id}` }]}
            meta={
              <GroupSummary group={project.data} connections={connections} />
            }
            actions={
              <>
                <TrackSessionButton />
                {project.data.archivedAt === null && scheduleList.length === 0 ? (
                  <Button onClick={() => newTask(projectId, "repeat")}>New schedule</Button>
                ) : null}
                {project.data.archivedAt === null ? <Button variant="primary" onClick={() => newTask(projectId, startingMode)}>
                  <IconPlus /> New task
                </Button> : null}
              </>
            }
          />
          <div className="flex flex-col gap-5">
            <ProjectGroupWorkspaces group={project.data} connections={connections} />
            {scheduleList.length === 0 && schedules.error === null ? null : (
              <Panel
                flush
                title="Schedules"
                meta={<span className="tabular">{scheduleList.length}</span>}
                actions={
                  project.data.archivedAt === null ? <Button small variant="ghost" onClick={() => newTask(projectId, "repeat")}>
                    <IconPlus size={12} /> New schedule
                  </Button> : undefined
                }
              >
                <QueryState isPending={false} error={schedules.error} hasData={schedules.data !== undefined}>
                  <ScheduleRows schedules={scheduleList} />
                </QueryState>
              </Panel>
            )}
            <Panel
              flush
              title="Tasks"
              {...(taskCount === undefined ? {} : { meta: taskCount })}
              actions={
                <div className="flex max-w-full flex-wrap items-center gap-2">
                  <div className="shrink-0">
                    <Segmented label="Task scope" small options={SCOPES} value={scope} onChange={setScope} />
                  </div>
                  <div className="w-32 sm:w-40">
                    <Combobox
                      value={stateFilter}
                      onChange={(next) => setStateFilter(pick(TASK_STATES, next) ?? "")}
                      aria-label="Filter by state"
                      listLabel="States"
                      className="w-full"
                      small
                      options={[{ value: "", label: "All states" }, ...TASK_STATES.map((s) => ({ value: s, label: taskStateLabel(s) }))]}
                    />
                  </div>
                </div>
              }
            >
              <QueryState isPending={tasks.isPending} error={tasks.error} hasData={tasks.data !== undefined} stale={tasks.isPlaceholderData}>
                <TaskRows
                  tasks={tasks.data?.tasks ?? []}
                  emptyText={
                    state !== undefined
                      ? "No tasks in this state."
                      : scope === "closed"
                        ? "No done or archived tasks in this project."
                        : "No tasks in this project."
                  }
                />
              </QueryState>
            </Panel>
          </div>
        </>
      )}
    </QueryState>
  );
}
