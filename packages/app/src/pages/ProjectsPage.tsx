import { useMutation } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import type { PreferenceStorage } from "../platform/index.ts";
import { useInvalidateProjects, useProjectGroups, useTasks } from "../api/queries.ts";
import type { AddedWorkspace, ProjectGroup, Task } from "../api/types.ts";
import { AddWorkspaceDrawer } from "../components/AddWorkspace.tsx";
import { GroupMembershipDrawer, type MembershipAction } from "../components/GroupMembershipDrawer.tsx";
import { HarnessFacts } from "../components/HarnessPresentation.tsx";
import { IconPlus } from "../components/icons.tsx";
import { GroupSummary } from "../components/GroupWorkspaceDisplay.tsx";
import { useWorkspaceConnections } from "../components/WorkspaceDisplay.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import {
  Button,
  Drawer,
  ErrorText,
  Field,
  Hint,
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

/** The add drawer's success view: what the harness added and where Stage grouped it, then Move to project or Done. */
function AddedWorkspaceView({ result, connectionName }: { result: AddedWorkspace; connectionName: string }) {
  const line = useRef<HTMLParagraphElement>(null);
  useEffect(() => { line.current?.focus(); }, []);
  const { workspace, group } = result;
  const name = <strong className="font-medium">{workspace.title || workspace.path || workspace.workspaceId}</strong>;
  const where = <TextLink to={`/projects/${group.id}`} variant="rest">{group.title}</TextLink>;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p ref={line} tabIndex={-1} role="status" className="text-body wrap-anywhere">
        {result.existing
          ? <>{name} was already listed on {connectionName}. It is in {where}.</>
          : <>Added {name} on {connectionName} in {where}.</>}
      </p>
      {workspace.path ? <code className="wrap-anywhere text-meta">{workspace.path}</code> : null}
      {result.message ? <Hint className="wrap-anywhere">{result.message}</Hint> : null}
      <HarnessFacts facts={result.facts} />
    </div>
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
  const [adding, setAdding] = useState(false);
  const [moving, setMoving] = useState<{ action: MembershipAction; group: ProjectGroup } | null>(null);
  const [moved, setMoved] = useState<Pick<ProjectGroup, "id" | "title"> | null>(null);
  const movedLine = useRef<HTMLParagraphElement>(null);
  useEffect(() => { if (moved !== null) movedLine.current?.focus(); }, [moved]);
  const closeMove = useCallback(() => setMoving(null), []);
  const [added, setAdded] = useState<{ result: AddedWorkspace; connectionName: string } | null>(null);
  const closeAdd = () => {
    setAdding(false);
    setAdded(null);
  };
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
  // The added row in the refreshed groups, for Move to project.
  const addedGroup = added === null ? undefined : groups.find((g) => g.id === added.result.group.id);
  const addedWorkspace = added === null ? undefined : addedGroup?.members.find((m) =>
    m.connectionId === added.result.workspace.connectionId && m.workspaceId === added.result.workspace.workspaceId);
  const addedMember = addedGroup === undefined || addedWorkspace === undefined
    ? null : { action: { workspace: addedWorkspace, separate: false }, group: addedGroup };

  return (
    <>
      <PageHeader
        title="Projects"
        actions={
          <>
            <Segmented label="Projects filter" options={FILTER_OPTIONS} value={filter} onChange={setFilter} />
            <Segmented label="Sort projects" options={SORT_OPTIONS} value={sort} onChange={changeSort} />
            <TrackSessionButton />
            <Button onClick={() => { setMoved(null); setAdding(true); }}>
              <IconPlus /> Add workspace
            </Button>
            <Button variant="primary" onClick={() => setCreating(true)}>
              <IconPlus /> New project
            </Button>
          </>
        }
      />
      {moved === null ? null : (
        <p ref={movedLine} tabIndex={-1} role="status" className="mb-3 text-small wrap-anywhere">
          Workspace moved to <TextLink to={`/projects/${moved.id}`}>{moved.title}</TextLink>.
        </p>
      )}
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
      {adding ? <AddWorkspaceDrawer onClose={closeAdd} onAdded={(result, connectionName) => setAdded({ result, connectionName })}
        done={added === null ? null : {
          body: <AddedWorkspaceView {...added} />,
          footer: <>
            <Button variant="primary" onClick={closeAdd}>Done</Button>
            <Button disabled={addedMember === null} onClick={() => {
              if (addedMember === null) return;
              closeAdd();
              setMoving(addedMember);
            }}>Move to project</Button>
          </>,
        }} /> : null}
      {moving === null ? null : <GroupMembershipDrawer action={moving.action} group={moving.group} connections={connections}
        onClose={closeMove} onApplied={(target) => { setMoved(target); closeMove(); }} />}
    </>
  );
}
