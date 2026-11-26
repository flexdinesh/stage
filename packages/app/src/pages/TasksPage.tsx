import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useSchedules, useTasks } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { useScope } from "../components/Scope.tsx";
import { GROUP_META, GroupPanel, GroupPanelSkeleton, ScheduledRows, StatStrip, useGroupFold } from "../components/TaskGroups.tsx";
import { CompletedRows, TaskRows, TaskRowsSkeleton } from "../components/TaskList.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { Button, EmptyState, PageHeader, Panel, QueryState, Segmented, Skeleton, buttonClass } from "../components/ui.tsx";
import {
  GROUP_KEYS,
  awaitingReview,
  filterByTree,
  inGroup,
  parseGroupKey,
  parseTreeFilter,
  type GroupKey,
  type TreeFilter,
} from "../lib/dashboard.ts";
import { scopedPageName } from "../lib/history.ts";
import { projectWorkspace } from "../lib/projects.ts";
import { LIVE_STATES } from "../lib/scope.ts";
import { mergeCompleted, completedWindowStart } from "../lib/completed.ts";

/** Open: work not yet closed (with the last 7 days completed); Closed: archived tasks and members of closed trees. */
type View = "open" | "closed" | "all";

const VIEWS: { value: View; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "all", label: "All" },
];

const TREE_FILTERS: { value: TreeFilter; label: string; short: string }[] = [
  { value: "any", label: "All tasks", short: "All" },
  { value: "none", label: "Not in a tree", short: "No tree" },
];

/** Stat strip cells (Scheduled lists as a group but has no cell). */
const STAT_GROUPS: readonly GroupKey[] = GROUP_KEYS.filter((k) => k !== "scheduled");

const FOLD_KEY = "stage-tasks-groups";

const parseView = (v: string | null): View => VIEWS.find((o) => o.value === v)?.value ?? "open";

export function TasksPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const tree = parseTreeFilter(params.get("tree"));
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
        name={scopedPageName("Tasks", scope.project?.title ?? null)}
        meta={scope.project === null ? "Tasks across projects" : projectWorkspace(scope.project)}
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
        <div className="ml-auto">
          <Segmented label="Tree membership" options={TREE_FILTERS} value={tree} onChange={(v) => setParam("tree", v === "any" ? null : v)} />
        </div>
      </div>
      <CompletionLinger>
        {view === "open" ? (
          <OpenTasks tree={tree} selected={parseGroupKey(params.get("state"))} onSelect={(k) => setParam("state", k)} />
        ) : (
          <ClosedTasks view={view} tree={tree} />
        )}
      </CompletionLinger>
    </>
  );
}

/** Open work: every task (or only those outside trees) by state; tree members link their tree. */
function OpenTasks({ tree, selected, onSelect }: { tree: TreeFilter; selected: GroupKey | null; onSelect: (key: string | null) => void }) {
  const scope = useScope();
  const scoped = scope.projectId !== null;
  const [since] = useState(() => completedWindowStart());
  const live = useTasks({ state: [...LIVE_STATES], ...scope.query });
  const completed = useTasks({ completedFrom: since, ...scope.query });
  const due = useSchedules({ dueWithin: "24h", ...scope.query });
  const schedules = due.data?.schedules ?? [];
  const fold = useGroupFold(FOLD_KEY);
  const { lingering } = useCompletionLinger();

  const merged = [...new Map([...(live.data?.tasks ?? []), ...(completed.data?.tasks ?? [])].map((t) => [t.id, t])).values()];
  const all = filterByTree(merged, tree);
  const of = (key: GroupKey, tasks: readonly Task[]) => tasks.filter((t) => inGroup(key, t, since, lingering));
  const completeAll = useCompleteAll(of("done", all).filter((t) => !lingering.has(t.id) && awaitingReview(t, lingering)));

  const counts: Record<GroupKey, number> = { needs: 0, done: 0, ready: 0, failed: 0, running: 0, backlog: 0, scheduled: 0, completed: 0 };
  for (const k of GROUP_KEYS) counts[k] = of(k, all).length + (k === "scheduled" ? schedules.length : 0);

  const groups = (selected === null ? GROUP_KEYS : [selected])
    .map((key) => ({ key, list: of(key, all) }))
    .filter(({ key, list }) => selected === key || list.length > 0 || (key === "scheduled" && schedules.length > 0));

  const body = (key: GroupKey, list: Task[]) => {
    if (key === "completed") return <CompletedRows items={mergeCompleted(list)} emptyText={GROUP_META.completed.empty} showProject={!scoped} />;
    if (key === "scheduled") return <ScheduledRows tasks={list} schedules={schedules} emptyText={GROUP_META.scheduled.empty} showProject={!scoped} />;
    return (
      <>
        {key === "done" ? <CompleteAllFailures state={completeAll} /> : null}
        <TaskRows tasks={list} emptyText={GROUP_META[key].empty} showProject={!scoped} showTree />
      </>
    );
  };

  return (
    <QueryState
      isPending={live.isPending || completed.isPending}
      error={live.error ?? completed.error}
      hasData={live.data !== undefined && completed.data !== undefined}
      stale={live.isPlaceholderData || completed.isPlaceholderData}
      skeleton={
        <>
          <StatStrip groups={STAT_GROUPS} meta={GROUP_META} counts={null} selected={selected} onToggle={(k) => onSelect(selected === k ? null : k)} />
          <Skeleton label="Loading tasks">
            <GroupPanelSkeleton showProject={!scoped} />
          </Skeleton>
        </>
      }
    >
      <StatStrip
        groups={STAT_GROUPS}
        meta={GROUP_META}
        counts={counts}
        selected={selected}
        onToggle={(k) => onSelect(selected === k ? null : k)}
      />
      <div className="flex flex-col gap-3.5">
        {groups.length === 0 ? (
          <EmptyState inset className="border border-line bg-surface">
            {tree === "none" && merged.length > 0
              ? "Every open task is in a tree. Trees lists them."
              : "No open tasks. Create one with New task, or let an agent plan a tree."}
          </EmptyState>
        ) : (
          groups.map(({ key, list }) => {
            const collapsed = selected !== key && fold.folded(key, list);
            return (
              <GroupPanel
                key={key}
                info={GROUP_META[key]}
                count={list.length + (key === "scheduled" ? schedules.length : 0)}
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

/** Closed or all work: every task (or only those outside trees) in one panel. */
function ClosedTasks({ view, tree }: { view: "closed" | "all"; tree: TreeFilter }) {
  const scope = useScope();
  const scoped = scope.projectId !== null;
  const tasks = useTasks({ closed: view === "closed" ? "true" : "all", ...scope.query });
  const all = tasks.data?.tasks ?? [];
  const listed = filterByTree(all, tree);
  const empty =
    tree === "none" && all.length > 0
      ? "Every task here is in a tree. Trees lists them."
      : view === "closed"
        ? "Nothing closed. Archived tasks and tasks of done or archived trees land here."
        : "No tasks yet. Create one with New task.";
  return (
    <QueryState
      isPending={tasks.isPending}
      error={tasks.error}
      hasData={tasks.data !== undefined}
      stale={tasks.isPlaceholderData}
      skeleton={
        <Panel flush title="Tasks">
          <Skeleton label="Loading tasks">
            <TaskRowsSkeleton showProject={!scoped} />
          </Skeleton>
        </Panel>
      }
    >
      <Panel flush title="Tasks" meta={<span className="tabular">{listed.length}</span>}>
        <TaskRows tasks={listed} emptyText={empty} showProject={!scoped} showTree />
      </Panel>
    </QueryState>
  );
}
