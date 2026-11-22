import { bindingsSummary } from "../lib/harnesses.ts";
import { importNotice, projectWorkspace } from "../lib/projects.ts";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useApi, usePlatform } from "../platform/context.tsx";
import type { PreferenceStorage } from "../platform/index.ts";
import { keys, useHarnesses, useProjects, useTasks } from "../api/queries.ts";
import type { Project, Task } from "../api/types.ts";
import { IconPlus } from "../components/icons.tsx";
import {
  Button,
  Chip,
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

const FORM_ID = "new-project-form";

function CreateProjectDrawer({ onClose }: { onClose: () => void }) {
  const api = useApi();
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const create = useMutation({
    mutationFn: () => api.createProject({ title: title.trim() }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.projects });
      onClose();
    },
  });
  const valid = title.trim() !== "";
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
        <Muted>Plan tasks now. Bind a harness workspace when you are ready to run them.</Muted>
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

const activityMs = (p: Project) => (p.lastActivityAt === null ? 0 : new Date(p.lastActivityAt).getTime());

/** Most recent activity first; projects without activity last, by name (server order, stable sort). */
const byRecent = (projects: Project[]) => projects.toSorted((a, b) => activityMs(b) - activityMs(a));

const ARCHIVED_OPTIONS = [
  { value: "active", label: "active" },
  { value: "all", label: "incl. archived" },
] as const;

/** Project row grid: name over path, harness chip, counts (below the name on phone). Shared with the skeleton. */
const PROJECT_ROW = "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 md:grid-cols-[minmax(0,1fr)_auto_128px]";

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
              <SkeletonBar className="h-[22px] w-9 justify-self-end md:justify-self-start" />
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
  const [filter, setFilter] = useState<"active" | "all">("active");
  const { storage } = usePlatform();
  const [sort, setSort] = useState<Sort>(() => readSort(storage));
  const [creating, setCreating] = useState(false);
  const projects = useProjects(filter === "all" ? "all" : "false");
  const tasks = useTasks();
  const fetched = projects.data?.projects ?? [];
  const list = sort === "recent" ? byRecent(fetched) : fetched;
  const changeSort = (next: Sort) => {
    setSort(next);
    // Storage unavailable (private mode): sort still applies for this page view.
    storage.set(SORT_KEY, next);
  };
  const counts = tasks.data === undefined ? null : countByProject(tasks.data.tasks);
  const { kindName } = useHarnesses();
  const notice = importNotice(projects.data?.imports ?? [], kindName);

  return (
    <>
      <PageHeader
        title="Projects"
        meta={[
          `${projects.data === undefined ? "…" : list.length} projects`,
          `archived ${filter === "all" ? "shown" : "hidden"}`,
          ...(notice === null ? [] : [notice]),
        ].join(" · ")}
        actions={
          <>
            <Segmented label="Sort projects" options={SORT_OPTIONS} value={sort} onChange={changeSort} />
            <Segmented label="Archived filter" options={ARCHIVED_OPTIONS} value={filter} onChange={setFilter} />
            <Button variant="primary" onClick={() => setCreating(true)}>
              <IconPlus /> New project
            </Button>
          </>
        }
      />
      <QueryState
        isPending={projects.isPending}
        error={projects.error}
        stale={projects.isPlaceholderData}
        skeleton={<ProjectRowsSkeleton title={filter === "all" ? "All projects" : "Active projects"} />}
        empty={list.length === 0}
        emptyText="No projects yet. Create one to start planning."
      >
        <Panel flush title={filter === "all" ? "All projects" : "Active projects"} meta={list.length}>
          <RowList>
            {list.map((p) => {
              const c = counts?.get(p.id) ?? { total: 0, active: 0 };
              return (
                <Row key={p.id} className={PROJECT_ROW}>
                  <div className="min-w-0">
                    <TextLink to={`/projects/${p.id}`} className="block truncate text-body font-medium">
                      {p.title}
                    </TextLink>
                    <Meta className="block truncate" title={projectWorkspace(p)}>
                      {projectWorkspace(p)}
                    </Meta>
                  </div>
                  <div className="flex items-center gap-1.5 justify-self-end md:justify-self-start">
                    {p.archivedAt === null ? null : <Chip tone="idle">archived</Chip>}
                    <Chip>{bindingsSummary(p.bindings, kindName)}</Chip>
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
          </RowList>
        </Panel>
      </QueryState>
      {creating ? <CreateProjectDrawer onClose={() => setCreating(false)} /> : null}
    </>
  );
}
