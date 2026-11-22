import { useState } from "react";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { Link } from "react-router";
import { useProjects, useSchedules, useT3Status, useOpenCodeStatus, useHarnesses, useTasks } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { IconCheck } from "../components/icons.tsx";
import { useScope } from "../components/Scope.tsx";
import { GROUP_META, GroupPanel, GroupPanelSkeleton, StatCount, useGroupFold } from "../components/TaskGroups.tsx";
import { TaskRows } from "../components/TaskList.tsx";
import { T3Alert } from "../components/T3Setup.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { ATTENTION_GROUPS, awaitingReview, inGroup, type GroupKey } from "../lib/dashboard.ts";
import { projectWorkspace } from "../lib/projects.ts";
import { LIVE_STATES } from "../lib/scope.ts";
import { Alert, AppButton, PageHeader, QueryState, SectionHeading, Skeleton, SkeletonBar, TextLink, buttonClass, type ChipTone } from "../components/ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";

/** Per-device collapsed groups (`GroupFold`). */
const FOLD_KEY = "stage-dashboard-groups";

function NeedsOpenLink({ tasks }: { tasks: Task[] }) {
  // App links open one thread, so only a lone task gets one.
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

type InFlightCell = { key: GroupKey; label: string; tone: ChipTone; count: number | null };

/** Work in motion that asks nothing: counts that open the Tasks view filtered to them. A null count is loading. */
function InFlight({ cells }: { cells: InFlightCell[] }) {
  return (
    <section aria-labelledby="in-flight-heading" className="mt-6">
      <SectionHeading id="in-flight-heading" title="In flight" meta="no action needed" />
      <ul className="grid grid-cols-3 border border-line bg-surface">
        {cells.map((c) => (
          <li key={c.key} className="border-r border-line last:border-r-0">
            <Link
              to={`/tasks?state=${c.key}`}
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

/** A harness connection notice above the dashboard, spaced like T3Alert. */
/** Planning remains available before pairing and during connection failures. */
export function DashboardPage() {
  const t3 = useT3Status();
  const openCode = useOpenCodeStatus();
  const harnesses = useHarnesses();
  // The server's view (a token, or a saved/env T3 URL), as the top-bar control uses: a disconnected T3 stays quiet.
  const t3Configured = harnesses.activeConnection("t3")?.configured ?? false;
  return (
    <>
      {t3Configured ? (
        <T3Alert status={t3.data} />
      ) : openCode.data?.state === "unconfigured" ? (
        <Alert tone="need" className="mb-3.5">
          <TextLink variant="rest" to="/settings">Connect a harness</TextLink> to start tasks in T3 or OpenCode.
        </Alert>
      ) : null}
      {openCode.data?.state === "unavailable" ? (
        <Alert tone="need" className="mb-3.5">
          OpenCode is unavailable. <TextLink variant="rest" to="/settings">Check connection</TextLink>
        </Alert>
      ) : null}
      {openCode.data?.state === "rejected" ? (
        <Alert tone="need" className="mb-3.5">
          OpenCode no longer accepts Stage's sign-in. <TextLink variant="rest" to="/settings">Reconnect</TextLink>
        </Alert>
      ) : null}
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
  const dueSchedules = useSchedules({ dueWithin: "24h", ...scope.query });
  const fold = useGroupFold(FOLD_KEY);
  // Rows completed here linger in Awaiting review (completed chip, Undo) before leaving.
  const { lingering } = useCompletionLinger();
  // Completed is not listed here; any window works for the group test.
  const [since] = useState(() => new Date().toISOString());
  const all = tasks.data?.tasks ?? [];
  const of = (key: GroupKey) => all.filter((t) => inGroup(key, t, since, lingering));
  const completeAll = useCompleteAll(all.filter((t) => !lingering.has(t.id) && awaitingReview(t, lingering)));

  const groups = ATTENTION_GROUPS.map((key) => ({ key, list: of(key) })).filter((g) => g.list.length > 0);
  const owed = groups.reduce((n, g) => n + g.list.length, 0);
  const projectCount = projects.data?.projects.length;
  const where =
    scope.project !== null
      ? projectWorkspace(scope.project)
      : projectCount === undefined
        ? null
        : `${projectCount} project${projectCount === 1 ? "" : "s"}`;
  const metaText = [tasks.data === undefined ? null : owed === 0 ? "nothing needs you" : `${owed} need${owed === 1 ? "s" : ""} you`, where]
    .filter((s) => s !== null)
    .join(" · ");
  // Loading holds the meta line's height so the header does not grow when the counts land.
  const meta =
    tasks.data === undefined ? (
      <span className="skeleton-reveal inline-block align-middle">
        <SkeletonBar className="h-3 w-44" />
      </span>
    ) : metaText;
  const scheduledCount = (n: number) => n + (dueSchedules.data?.schedules.length ?? 0);
  const inFlight = (loaded: boolean): InFlightCell[] => [
    { key: "running", label: "Running", tone: "run", count: loaded ? of("running").length : null },
    { key: "scheduled", label: "Scheduled · 24h", tone: "idle", count: loaded ? scheduledCount(of("scheduled").length) : null },
    { key: "backlog", label: "Backlog", tone: "idle", count: loaded ? of("backlog").length : null },
  ];

  return (
    <>
      <PageHeader
        title={scope.project === null ? "Dashboard" : <><span className="text-muted">{scope.project.title} / </span>Dashboard</>}
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
        isPending={tasks.isPending}
        error={tasks.error}
        stale={tasks.isPlaceholderData}
        skeleton={
          <>
            <Skeleton label="Loading dashboard">
              <GroupPanelSkeleton showProject={!scoped} />
            </Skeleton>
            <InFlight cells={inFlight(false)} />
          </>
        }
      >
        {groups.length === 0 ? (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border border-line bg-surface px-4 py-5">
            <span className="text-ok">
              <IconCheck />
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-title font-semibold">Nothing needs you{scope.project === null ? "" : ` in ${scope.project.title}`}.</p>
              <p className="mt-0.5 text-small text-muted">Questions, finished runs to review, ready starts and failures land here.</p>
            </div>
            <Link to="/tasks" className={buttonClass("secondary")}>
              Browse tasks
            </Link>
          </div>
        ) : (
          <div className="flex flex-col gap-3.5">
            {groups.map(({ key, list }) => {
              const collapsed = fold.folded(key, list);
              return (
                <GroupPanel
                  key={key}
                  info={GROUP_META[key]}
                  count={list.length}
                  collapsed={collapsed}
                  onToggle={() => fold.toggle(key, list)}
                  {...(key === "needs" ? { actions: <NeedsOpenLink tasks={list} /> } : {})}
                  {...(key === "done" ? { actions: <CompleteAllActions state={completeAll} /> } : {})}
                >
                  {key === "done" ? <CompleteAllFailures state={completeAll} /> : null}
                  <TaskRows tasks={list} emptyText={GROUP_META[key].empty} showProject={!scoped} showTree />
                </GroupPanel>
              );
            })}
          </div>
        )}
        <InFlight cells={inFlight(true)} />
      </QueryState>
    </>
  );
}
