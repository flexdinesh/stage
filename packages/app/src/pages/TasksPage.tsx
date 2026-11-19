import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useSchedules, useTasks, useTree, useTrees } from "../api/queries.ts";
import type { Task, TreeSummary } from "../api/types.ts";
import { IconChevronDown, IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { useScope } from "../components/Scope.tsx";
import { GROUP_META, GroupPanel, ScheduledRows, StatStrip, useGroupFold } from "../components/TaskGroups.tsx";
import { SettledRows, TaskRows } from "../components/TaskList.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { TreeRow } from "../components/trees/TreeRow.tsx";
import { TreeView, TreeViewSkeleton } from "../components/trees/TreeView.tsx";
import { Button, ErrorText, PageHeader, Panel, QueryState, Segmented, buttonClass, cx } from "../components/ui.tsx";
import { GROUP_KEYS, awaitingReview, inGroup, parseGroupKey, splitByKind, type GroupKey } from "../lib/dashboard.ts";
import { projectWorkspace } from "../lib/projects.ts";
import { LIVE_STATES } from "../lib/scope.ts";
import { mergeSettled } from "../lib/settled.ts";
import { treeClosed } from "../lib/trees.ts";
import { usePlatform } from "../platform/context.tsx";

/** Open: work not yet closed (with the last 7 days completed); Closed: archived tasks and done or archived trees. */
type View = "open" | "closed" | "all";
type Grouping = "kind" | "state";

const VIEWS: { value: View; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "all", label: "All" },
];

const GROUPINGS: { value: Grouping; label: string }[] = [
  { value: "kind", label: "Kind" },
  { value: "state", label: "State" },
];

/** Stat strip cells (Scheduled lists as a group but has no cell). */
const STAT_GROUPS: readonly GroupKey[] = GROUP_KEYS.filter((k) => k !== "scheduled");

const FOLD_KEY = "stage-tasks-groups";
const GROUPING_KEY = "stage-tasks-grouping";
const SETTLED_DAYS = 7;

/** Start of the Completed window, floored to the hour so the query key stays stable between renders. */
function settledSince(): string {
  const d = new Date(Date.now() - SETTLED_DAYS * 24 * 3600 * 1000);
  d.setMinutes(0, 0, 0);
  return d.toISOString();
}

const parseView = (v: string | null): View => VIEWS.find((o) => o.value === v)?.value ?? "open";
const parseGrouping = (v: string | null): Grouping => (v === "state" ? "state" : "kind");

export function TasksPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const { storage } = usePlatform();
  const [grouping, setGroupingState] = useState(() => parseGrouping(storage.get(GROUPING_KEY)));
  const setGrouping = (g: Grouping) => {
    setGroupingState(g);
    storage.set(GROUPING_KEY, g);
  };
  const scope = useScope();
  const newTask = useNewTask();
  const setParam = (key: string, value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === null) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true },
    );

  return (
    <>
      <PageHeader
        title={scope.project === null ? "Tasks" : <><span className="text-muted">{scope.project.title} / </span>Tasks</>}
        meta={scope.project === null ? "Trees and tasks across projects" : projectWorkspace(scope.project)}
        actions={
          <>
            <TrackSessionButton />
            {scope.project === null ? null : (
              <Link to={`/projects/${scope.project.id}`} className={buttonClass("secondary")}>
                Project settings
              </Link>
            )}
            <Button variant="primary" onClick={() => newTask(scope.projectId ?? undefined)} className="max-md:hidden">
              <IconPlus /> New task
            </Button>
          </>
        }
      />
      <div className="mb-3.5 flex flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented label="Tasks to show" options={VIEWS} value={view} onChange={(v) => setParam("view", v === "open" ? null : v)} />
        <div className="ml-auto flex items-center gap-2">
          <span className="font-mono text-meta-sm text-muted">group by</span>
          <Segmented label="Group by" options={GROUPINGS} value={grouping} onChange={setGrouping} />
        </div>
      </div>
      <CompletionLinger>
        {view === "open" ? (
          <OpenTasks grouping={grouping} selected={parseGroupKey(params.get("state"))} onSelect={(k) => setParam("state", k)} />
        ) : (
          <ClosedTasks view={view} grouping={grouping} />
        )}
      </CompletionLinger>
    </>
  );
}

/** A tree row that expands to its members in level order. */
function TreeItem({ tree }: { tree: TreeSummary }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="border-b border-line last:border-b-0">
      <div className="flex items-stretch">
        <button
          type="button"
          aria-expanded={open}
          aria-label={`${open ? "Hide" : "Show"} tasks of ${tree.title}`}
          onClick={() => setOpen((o) => !o)}
          className="flex w-9 shrink-0 items-center justify-center text-muted transition-colors duration-150 hover:bg-panel hover:text-fg"
        >
          <span className={cx("inline-flex transition-transform duration-150", !open && "-rotate-90")}>
            <IconChevronDown size={12} />
          </span>
        </button>
        <div className="min-w-0 flex-1 [&>div]:pl-0">
          <TreeRow tree={tree} />
        </div>
      </div>
      {open ? <TreeMembers id={tree.id} /> : null}
    </li>
  );
}

function TreeMembers({ id }: { id: number }) {
  const tree = useTree(id);
  const navigate = useNavigate();
  return (
    <div className="border-t border-line bg-bg">
      {tree.isPending ? (
        <TreeViewSkeleton />
      ) : tree.error !== null || tree.data === undefined ? (
        <div className="p-3">
          <ErrorText error={tree.error} />
        </div>
      ) : (
        <TreeView tree={tree.data} selected={null} onSelect={(task) => void navigate(`/tasks/${task}`)} />
      )}
    </div>
  );
}

function TreesPanel({ trees, title = "Trees" }: { trees: readonly TreeSummary[]; title?: string }) {
  if (trees.length === 0) return null;
  return (
    <Panel flush title={title} meta={<span className="tabular">{trees.length}</span>}>
      <ul>
        {trees.map((t) => (
          <TreeItem key={t.id} tree={t} />
        ))}
      </ul>
    </Panel>
  );
}

function SectionTitle({ title, meta }: { title: string; meta: string }) {
  return (
    <div className="mt-2 flex items-baseline gap-3">
      <h2 className="text-title font-semibold">{title}</h2>
      <span className="font-mono text-meta-sm text-muted">{meta}</span>
    </div>
  );
}

/** Open work: Kind lists trees, then tasks outside trees by state; State lists every task by state (tree chips). */
function OpenTasks({
  grouping,
  selected,
  onSelect,
}: {
  grouping: Grouping;
  selected: GroupKey | null;
  onSelect: (key: string | null) => void;
}) {
  const scope = useScope();
  const scoped = scope.projectId !== null;
  const [since] = useState(settledSince);
  const live = useTasks({ state: [...LIVE_STATES], ...scope.query });
  const settled = useTasks({ settledFrom: since, ...scope.query });
  // Trees with work left, plus trees completed inside the window (listed in Completed).
  const trees = useTrees({ settledFrom: since, ...scope.query });
  const due = useSchedules({ dueWithin: "24h", ...scope.query });
  const schedules = due.data?.schedules ?? [];
  const fold = useGroupFold(FOLD_KEY);
  const { lingering } = useCompletionLinger();

  const all = [...new Map([...(live.data?.tasks ?? []), ...(settled.data?.tasks ?? [])].map((t) => [t.id, t])).values()];
  const openTrees = (trees.data?.trees ?? []).filter((t) => t.status === "open");
  const kind = splitByKind(all, openTrees);
  const byKind = grouping === "kind" && selected === null;
  const listed = byKind ? kind.standalone : all;
  const of = (key: GroupKey, tasks: readonly Task[]) => tasks.filter((t) => inGroup(key, t, since, lingering));
  const completeAll = useCompleteAll(of("done", listed).filter((t) => !lingering.has(t.id) && awaitingReview(t, lingering)));

  const counts: Record<GroupKey, number> = { needs: 0, done: 0, ready: 0, failed: 0, running: 0, backlog: 0, scheduled: 0, settled: 0 };
  for (const k of GROUP_KEYS) counts[k] = of(k, all).length + (k === "scheduled" ? schedules.length : 0);

  const groups = (selected === null ? GROUP_KEYS : [selected])
    .map((key) => ({ key, list: of(key, listed) }))
    .filter(({ key, list }) => selected === key || list.length > 0 || (key === "scheduled" && schedules.length > 0) || (key === "settled" && byKind && kind.settled.length > 0));

  const body = (key: GroupKey, list: Task[]) => {
    if (key === "settled")
      return (
        <SettledRows
          items={mergeSettled(list, byKind ? kind.settled : [])}
          emptyText={GROUP_META.settled.empty}
          showProject={!scoped}
        />
      );
    if (key === "scheduled") return <ScheduledRows tasks={list} schedules={schedules} emptyText={GROUP_META.scheduled.empty} showProject={!scoped} />;
    return (
      <>
        {key === "done" ? <CompleteAllFailures state={completeAll} /> : null}
        <TaskRows tasks={list} emptyText={GROUP_META[key].empty} showProject={!scoped} showTree={!byKind} />
      </>
    );
  };

  return (
    <QueryState isPending={live.isPending || settled.isPending} error={live.error ?? settled.error}>
      <StatStrip groups={STAT_GROUPS} counts={counts} selected={selected} onToggle={(k) => onSelect(selected === k ? null : k)} />
      {trees.error === null ? null : (
        <div className="mb-3">
          <ErrorText error={trees.error} />
        </div>
      )}
      <div className="flex flex-col gap-3.5">
        {byKind ? (
          <>
            <TreesPanel trees={kind.active} />
            {kind.active.length === 0 ? null : <SectionTitle title="Tasks" meta="not in a tree" />}
          </>
        ) : null}
        {groups.length === 0 ? (
          <p className="border border-line bg-surface px-3 py-2.5 text-small text-muted">
            {byKind && kind.active.length > 0
              ? "Every open task is in a tree."
              : "No open tasks. Create one with New task, or let an agent plan a tree."}
          </p>
        ) : (
          groups.map(({ key, list }) => {
            const collapsed = selected !== key && fold.folded(key, list);
            const count = list.length + (key === "scheduled" ? schedules.length : 0) + (key === "settled" && byKind ? kind.settled.length : 0);
            // Kind lists tree members inside their tree; say how many this group leaves there.
            const inTrees = byKind ? counts[key] - list.length - (key === "scheduled" ? schedules.length : 0) : 0;
            return (
              <GroupPanel
                key={key}
                group={key}
                count={count}
                {...(inTrees > 0 ? { meta: `${inTrees} in trees` } : {})}
                collapsed={collapsed}
                {...(selected === key ? {} : { onToggle: () => fold.toggle(key, list) })}
                {...(key === "done" ? { actions: <CompleteAllActions state={completeAll} /> } : {})}
              >
                {body(key, list)}
              </GroupPanel>
            );
          })
        )}
      </div>
    </QueryState>
  );
}

/** Closed or all work: Kind lists trees, then tasks outside them; State lists every task in one panel. */
function ClosedTasks({ view, grouping }: { view: "closed" | "all"; grouping: Grouping }) {
  const scope = useScope();
  const scoped = scope.projectId !== null;
  const tasks = useTasks({ closed: view === "closed" ? "true" : "all", ...scope.query });
  const trees = useTrees({ status: "all", ...scope.query });
  // Closed trees: marked done, completed, or archived.
  const treeList = (trees.data?.trees ?? []).filter((t) => view === "all" || treeClosed(t) || t.settledAt !== null);
  const all = tasks.data?.tasks ?? [];
  const byKind = grouping === "kind";
  const listed = byKind ? splitByKind(all, treeList).standalone : all;
  const empty =
    view === "closed" ? "Nothing closed. Archived tasks and done or archived trees land here." : "No tasks yet. Create one with New task.";
  return (
    <QueryState isPending={tasks.isPending || trees.isPending} error={tasks.error ?? trees.error}>
      <div className="flex flex-col gap-3.5">
        {byKind ? <TreesPanel trees={treeList} /> : null}
        <Panel flush title={byKind ? "Tasks not in a tree" : "Tasks"} meta={<span className="tabular">{listed.length}</span>}>
          <TaskRows tasks={listed} emptyText={byKind && treeList.length > 0 ? "Every task here is in a tree." : empty} showProject={!scoped} showTree={!byKind} />
        </Panel>
      </div>
    </QueryState>
  );
}
