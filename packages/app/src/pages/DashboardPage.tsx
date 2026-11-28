import { useState } from "react";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { Link } from "react-router";
import { useProjects, useSchedules, useHarnesses, useTasks, useTrees } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { IconCheck } from "../components/icons.tsx";
import { useScope } from "../components/Scope.tsx";
import { GROUP_META, GroupPanel, GroupPanelSkeleton, StatCount, useGroupFold } from "../components/TaskGroups.tsx";
import { TaskRows } from "../components/TaskList.tsx";
import { TreeItem } from "../components/trees/TreeItem.tsx";
import { TreeRowsSkeleton } from "../components/trees/TreeRow.tsx";
import { useStatus } from "../components/StatusRow.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { awaitingReview, dashboardGroups, dashboardProblems, harnessNotice, inGroup, needsYouCount, type GroupKey } from "../lib/dashboard.ts";
import { scopedPageName } from "../lib/history.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { groupWorkspaceSummary } from "../lib/projectGroups.ts";
import { LIVE_STATES } from "../lib/scope.ts";
import { Alert, AppButton, PageHeader, QueryState, RowList, SectionHeading, Skeleton, SkeletonBar, TextLink, buttonClass, type ChipTone } from "../components/ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";

/** Per-device collapsed groups (`GroupFold`). */
const FOLD_KEY = "stage-dashboard-groups";

function NeedsOpenLink({ tasks }: { tasks: Task[] }) {
  // App links open one session, so only a lone task (with no tree beside it) gets one.
  const destination = useSessionDestination();
  const { kindName } = useHarnesses();
  const href = tasks.length === 1 ? (tasks[0] === undefined ? null : destination(tasks[0])) : null;
  if (href === null || !tasks[0]) return null;
  return (
    <AppButton href={href} small variant="ghost">
      Open in {kindName(tasks[0].harness)}
    </AppButton>
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

type InFlightCell = { key: GroupKey; label: string; tone: ChipTone; count: number | null };

/**
 * Queued work that asks nothing, outside trees (a tree shows through its row): counts that open the Tasks view filtered
 * to them. A null count is loading.
 */
function InFlight({ cells }: { cells: InFlightCell[] }) {
  return (
    <section aria-labelledby="in-flight-heading" className="mt-6">
      <SectionHeading id="in-flight-heading" title="In flight" meta="no action needed" />
      <ul className="grid grid-cols-2 border border-line bg-surface">
        {cells.map((c) => (
          <li key={c.key} className="border-r border-line last:border-r-0">
            <Link
              to={`/tasks?state=${c.key}&tree=none`}
              className="flex flex-col gap-1 px-3 py-2.5 transition-colors duration-150 hover:bg-panel"
            >
              <StatCount tone={c.tone} count={c.count} />
              <span className="label-caps text-muted">{c.label}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * Above the Dashboard: "Connect a harness" while none is set up, else one alert per connection problem the status
 * button shows (every set-up connection on every machine, read on its machine), linking to Settings. Planning never
 * waits on a connection.
 */
function HarnessNotices() {
  const harnesses = useHarnesses();
  // The server's view (a set-up connection of any harness), as the status control uses: a disconnected harness stays quiet.
  const notice = harnessNotice(harnesses.loaded ? harnesses.list : null);
  const { row } = useStatus();
  if (notice === "connect") {
    return (
      <Alert tone="need" className="mb-3.5">
        <TextLink variant="rest" to={SETTINGS_PATH.connections}>Connect a harness</TextLink> to start tasks.
      </Alert>
    );
  }
  const problems = dashboardProblems(row.items);
  if (problems.length === 0) return null;
  return (
    <div className="mb-3.5 flex flex-col gap-2">
      {problems.map((p) => (
        <Alert key={p.key} tone={p.tone}>
          {p.label}. <TextLink variant="rest" to={SETTINGS_PATH.connections}>Fix in Settings</TextLink>
        </Alert>
      ))}
    </div>
  );
}

export function DashboardPage() {
  return (
    <>
      <HarnessNotices />
      <CompletionLinger>
        <Dashboard />
      </CompletionLinger>
    </>
  );
}

function Dashboard() {
  const scope = useScope();
  const scoped = scope.projectId !== null;
  const projects = useProjects();
  const tasks = useTasks({ state: [...LIVE_STATES], ...scope.query });
  const trees = useTrees(scope.query);
  const dueSchedules = useSchedules({ dueWithin: "24h", ...scope.query });
  const fold = useGroupFold(FOLD_KEY);
  // Rows completed here linger in Awaiting review (completed chip, Undo) before leaving.
  const { lingering } = useCompletionLinger();
  // Completed is not listed here; any window works for the group test.
  const [since] = useState(() => new Date().toISOString());
  const all = tasks.data?.tasks ?? [];
  // Tree members show only through their tree's row.
  const loose = all.filter((t) => t.treeId === null);
  const of = (key: GroupKey) => loose.filter((t) => inGroup(key, t, since, lingering));
  const completeAll = useCompleteAll(loose.filter((t) => !lingering.has(t.id) && awaitingReview(t, lingering)));

  const loaded = tasks.data !== undefined && trees.data !== undefined;
  const groups = dashboardGroups(all, trees.data?.trees ?? [], since, lingering);
  const owed = needsYouCount(groups);
  const projectCount = projects.data?.projects.length;
  const where =
    scope.project !== null
      ? groupWorkspaceSummary(scope.project)
      : projectCount === undefined
        ? null
        : `${projectCount} project${projectCount === 1 ? "" : "s"}`;
  const metaText = [loaded ? (owed === 0 ? "nothing needs you" : `${owed} need${owed === 1 ? "s" : ""} you`) : null, where]
    .filter((s) => s !== null)
    .join(" · ");
  // Loading holds the meta line's height so the header does not grow when the counts land.
  const meta = loaded ? (
    metaText
  ) : (
    <span className="skeleton-reveal inline-block align-middle">
      <SkeletonBar className="h-3 w-44" />
    </span>
  );
  const scheduledCount = (n: number) => n + (dueSchedules.data?.schedules.length ?? 0);
  const inFlight = (ready: boolean): InFlightCell[] => [
    { key: "scheduled", label: "Scheduled · 24h", tone: "idle", count: ready ? scheduledCount(of("scheduled").length) : null },
    { key: "backlog", label: "Backlog", tone: "idle", count: ready ? of("backlog").length : null },
  ];

  return (
    <>
      <PageHeader
        title={scope.project === null ? "Dashboard" : <><span className="text-muted">{scope.project.title} / </span>Dashboard</>}
        name={scopedPageName("Dashboard", scope.project?.title ?? null)}
        {...(meta === "" ? {} : { meta })}
        actions={
          <>
            <TrackSessionButton />
            {scope.project === null ? null : (
              <Link to={`/projects/${scope.project.id}`} className={buttonClass("secondary")}>
                Project settings
              </Link>
            )}
          </>
        }
      />
      <QueryState
        isPending={tasks.isPending || trees.isPending}
        error={tasks.error ?? trees.error}
        hasData={loaded}
        stale={tasks.isPlaceholderData || trees.isPlaceholderData}
        skeleton={
          <>
            <Skeleton label="Loading dashboard">
              <div className="flex flex-col gap-3.5">
                <GroupPanelSkeleton showProject={!scoped} />
                <TreeRowsSkeleton count={2} />
              </div>
            </Skeleton>
            <InFlight cells={inFlight(false)} />
          </>
        }
      >
        {groups.every((g) => g.key === "running") ? (
          <div className="mb-3.5 flex flex-wrap items-center gap-x-4 gap-y-2 border border-line bg-surface px-4 py-5">
            <span className="text-ok">
              <IconCheck />
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-title font-semibold">Nothing needs you{scope.project === null ? "" : ` in ${scope.project.title}`}.</p>
              <p className="mt-0.5 text-small text-muted">Questions, finished runs to review, failures and ready starts land here.</p>
            </div>
            <Link to="/tasks" className={buttonClass("secondary")}>
              Browse tasks
            </Link>
          </div>
        ) : null}
        <div className="flex flex-col gap-3.5">
          {groups.map(({ key, trees: groupTrees, tasks: list }) => {
            // A collapsed Needs input reopens on a new question, a tree member's included.
            const treeIds = new Set(groupTrees.map((t) => t.id));
            const folding = [...list, ...all.filter((t) => t.state === "needs_input" && t.treeId !== null && treeIds.has(t.treeId))];
            const collapsed = fold.folded(key, folding);
            return (
              <GroupPanel
                key={key}
                info={GROUP_META[key]}
                count={groupTrees.length + list.length}
                meta={groupTrees.length === 0 || list.length === 0 ? undefined : `${plural(groupTrees.length, "tree")} · ${plural(list.length, "task")}`}
                collapsed={collapsed}
                onToggle={() => fold.toggle(key, folding)}
                {...(key === "needs" && groupTrees.length === 0 ? { actions: <NeedsOpenLink tasks={list} /> } : {})}
                {...(key === "done" ? { actions: <CompleteAllActions state={completeAll} /> } : {})}
              >
                {key === "done" ? <CompleteAllFailures state={completeAll} /> : null}
                {groupTrees.length === 0 ? null : (
                  <RowList>
                    {groupTrees.map((t) => (
                      <TreeItem key={t.id} tree={t} />
                    ))}
                  </RowList>
                )}
                {list.length === 0 ? null : (
                  <div className={groupTrees.length === 0 ? undefined : "border-t border-line"}>
                    <TaskRows tasks={list} emptyText={GROUP_META[key].empty} showProject={!scoped} />
                  </div>
                )}
              </GroupPanel>
            );
          })}
        </div>
        <InFlight cells={inFlight(true)} />
      </QueryState>
    </>
  );
}
