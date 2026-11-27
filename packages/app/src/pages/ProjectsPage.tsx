import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import type { PreferenceStorage } from "../platform/index.ts";
import { useInvalidateProjects, useProjectGroups, useTasks } from "../api/queries.ts";
import type { ProjectGroup, Task } from "../api/types.ts";
import { IconPlus } from "../components/icons.tsx";
import { GroupSummary } from "../components/GroupWorkspaceDisplay.tsx";
import { useWorkspaceConnections } from "../components/WorkspaceDisplay.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import {
  Button,
  Drawer,
  ErrorText,
  Field,
  Input,
  Meta,
  Muted,
  PageHeader,
  Panel,
  QueryState,
  Row,
  RowList,
  Segmented,
  Skeleton,
  SkeletonBar,
  TextLink,
  cx,
} from "../components/ui.tsx";
import { dateTime, relativeTime } from "../lib/format.ts";
import { useSearchParam } from "../lib/params.ts";
import { duplicateGroupTitles, isCurrentGroup } from "../lib/projectGroups.ts";

const FORM_ID = "new-project-form";

function CreateProjectDrawer({ onClose }: { onClose: () => void }) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const create = useMutation({
    mutationFn: () => api.createProject({ title: title.trim() }),
    onSuccess: refresh,
  });
  const valid = title.trim() !== "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (valid && !create.isPending) create.mutate(undefined, {
      onSuccess: (project) => {
        onClose();
        void navigate(`/projects/${project.id}`);
      },
    });
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
        <Muted>Create a project to plan work or group workspaces.</Muted>
        <ErrorText error={create.error} />
      </form>
    </Drawer>
  );
}

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

const SORT_OPTIONS = [
  { value: "name", label: "name" },
  { value: "recent", label: "recent" },
] as const;
type Sort = (typeof SORT_OPTIONS)[number]["value"];

const SORT_KEY = "stage-projects-sort";

function readSort(storage: PreferenceStorage): Sort {
  const v = storage.get(SORT_KEY);
  return SORT_OPTIONS.find((o) => o.value === v)?.value ?? "name";
}

const activityMs = (p: ProjectGroup) => (p.lastActivityAt === null ? 0 : new Date(p.lastActivityAt).getTime());

/** Most recent activity first; projects without activity last, by name (server order, stable sort). */
const byRecent = (projects: ProjectGroup[]) => projects.toSorted((a, b) => activityMs(b) - activityMs(a));

const FILTERS = ["active", "history", "all"] as const;
type Filter = (typeof FILTERS)[number];

const FILTER_OPTIONS: { value: Filter; label: string }[] = [
  { value: "active", label: "Current" },
  { value: "history", label: "History" },
  { value: "all", label: "All" },
];

const PROJECT_ROW = "grid min-w-0 items-center gap-x-3 gap-y-1 md:grid-cols-[minmax(0,1fr)_128px]";

const NAME_WIDTHS = ["w-24", "w-16", "w-28", "w-20", "w-32"];

function ProjectRowsSkeleton({ title }: { title: string }) {
  return (
    <Panel flush title={title}>
      <Skeleton label="Loading projects">
        <RowList>
          {NAME_WIDTHS.map((w, i) => (
            <Row key={i} className={PROJECT_ROW}>
              <div className="flex min-w-0 flex-col gap-1.5 py-0.5">
                <SkeletonBar className={cx("h-4", w)} />
                <SkeletonBar className="h-3 w-56 max-w-full" />
              </div>
              <div className="flex flex-col gap-1.5 py-0.5">
                <SkeletonBar className="h-3 w-14" />
                <SkeletonBar className="h-3 w-20" />
              </div>
            </Row>
          ))}
        </RowList>
      </Skeleton>
    </Panel>
  );
}

export function ProjectsPage() {
  // In the address, so Back keeps the filter.
  const [filter, setFilter] = useSearchParam("archived", FILTERS, "active");
  const { storage } = usePlatform();
  const [sort, setSort] = useState<Sort>(() => readSort(storage));
  const [creating, setCreating] = useState(false);
  const projects = useProjectGroups();
  const tasks = useTasks();
  const groups = projects.data?.groups ?? [];
  const fetched = groups.filter((group) => filter === "all" || isCurrentGroup(group) === (filter === "active"));
  const list = sort === "recent" ? byRecent(fetched) : fetched;
  const duplicates = duplicateGroupTitles(list);
  const title = filter === "history" ? "Project history" : filter === "all" ? "All projects" : "Current projects";
  const empty = filter === "active" ? <>No projects with current workspaces. <TextLink to="/projects?archived=history">View project history</TextLink> or <TextLink to="/settings/connections">connect a harness</TextLink>.</>
    : filter === "history" ? "No empty, retired or archived projects." : "No projects yet.";
  const changeSort = (next: Sort) => {
    setSort(next);
    // Storage unavailable (private mode): sort still applies for this page view.
    storage.set(SORT_KEY, next);
  };
  const counts = tasks.data === undefined ? null : countByProject(tasks.data.tasks);
  const connections = useWorkspaceConnections();

  return (
    <>
      <PageHeader
        title="Projects"
        actions={
          <>
            <Segmented label="Projects filter" options={FILTER_OPTIONS} value={filter} onChange={setFilter} />
            <Segmented label="Sort projects" options={SORT_OPTIONS} value={sort} onChange={changeSort} />
            <TrackSessionButton />
            <Button variant="primary" onClick={() => setCreating(true)}>
              <IconPlus /> New project
            </Button>
          </>
        }
      />
      <QueryState
        isPending={projects.isPending}
        error={projects.error}
        hasData={projects.data !== undefined}
        skeleton={<ProjectRowsSkeleton title={title} />}
      >
        <Panel flush title={title} meta={list.length}>
          {list.length === 0 ? <div className="p-3"><Muted>{empty}</Muted></div> : <RowList>
            {list.map((p) => {
              const c = counts?.get(p.id) ?? { total: 0, active: 0 };
              const duplicate = duplicates.has(p.title.toLowerCase());
              return (
                <Row key={p.id} className={PROJECT_ROW}>
                  <div className="flex min-w-0 flex-col gap-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <TextLink to={`/projects/${p.id}`} className="wrap-anywhere text-body font-medium"
                        aria-label={duplicate ? `${p.title}, project #${p.id}` : undefined}>{p.title}</TextLink>
                      {duplicate ? <Meta>#{p.id}</Meta> : null}
                    </div>
                    <GroupSummary group={p} connections={connections} disambiguate={duplicate} />
                  </div>
                  <Meta className="flex flex-col tabular">
                    <span>
                      {counts === null ? null : (
                        <>
                          {c.total} {c.total === 1 ? "task" : "tasks"}
                          {c.active === 0 ? null : <span className="text-fg"> · {c.active} active</span>}
                        </>
                      )}
                    </span>
                    {p.lastActivityAt === null ? null : (
                      <span title={`Last activity ${dateTime(p.lastActivityAt)}`}>active {relativeTime(p.lastActivityAt)}</span>
                    )}
                  </Meta>
                </Row>
              );
            })}
          </RowList>}
        </Panel>
      </QueryState>
      {creating ? <CreateProjectDrawer onClose={() => setCreating(false)} /> : null}
    </>
  );
}
