import { useState } from "react";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { Link } from "react-router";
import { useProjects, useSchedules, useT3Status, useOpenCodeStatus, useHarnesses, useTasks } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { IconCheck } from "../components/icons.tsx";
import { useScope } from "../components/Scope.tsx";
import { GROUP_META, GroupPanel, useGroupFold } from "../components/TaskGroups.tsx";
import { TaskRows } from "../components/TaskList.tsx";
import { T3Alert } from "../components/T3Setup.tsx";
import { CompleteAllActions, CompleteAllFailures, CompletionLinger, useCompleteAll, useCompletionLinger } from "../components/QuickComplete.tsx";
import { ATTENTION_GROUPS, awaitingReview, inGroup, type GroupKey } from "../lib/dashboard.ts";
import { projectWorkspace } from "../lib/projects.ts";
import { LIVE_STATES } from "../lib/scope.ts";
import { AppButton, PageHeader, QueryState, buttonClass, cx, toneClass, type ChipTone } from "../components/ui.tsx";
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

/** Work in motion that asks nothing: counts that open the Tasks view filtered to them. */
function InFlight({ cells }: { cells: { key: GroupKey; label: string; tone: ChipTone; count: number }[] }) {
  return (
    <section aria-labelledby="in-flight" className="mt-6">
      <div className="mb-2 flex items-baseline gap-3">
        <h2 id="in-flight" className="text-title font-semibold">
          In flight
        </h2>
        <span className="font-mono text-meta-sm text-muted">no action needed</span>
      </div>
      <ul className="grid grid-cols-3 border border-line bg-surface">
        {cells.map((c) => (
          <li key={c.key} className="border-r border-line last:border-r-0">
            <Link
              to={`/tasks?state=${c.key}`}
              className={cx(toneClass(c.tone), "flex flex-col gap-1 px-3 py-2.5 transition-colors duration-150 hover:bg-panel")}
            >
              <span className="flex items-center gap-2">
                {c.count > 0 ? <span aria-hidden className="size-1.5 bg-[var(--c)]" /> : null}
                <span className={cx("tabular text-title font-bold", c.count === 0 ? "text-muted" : "text-fg")}>{c.count}</span>
              </span>
              <span className="label-caps text-muted">{c.label}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Planning remains available before pairing and during connection failures. */
export function DashboardPage() {
  const t3 = useT3Status();
  const openCode = useOpenCodeStatus();
  const harnesses = useHarnesses();
  // The server's view (a token, or a saved/env T3 URL), as the top-bar control uses: a disconnected T3 stays quiet.
  const t3Configured = harnesses.activeConnection("t3")?.configured ?? false;
  return (
    <>
      {t3Configured ? <T3Alert status={t3.data} /> : openCode.data?.state === "unconfigured" ? <p className="mb-3 text-small"><Link className="underline" to="/settings">Connect a harness</Link> to start tasks in T3 or OpenCode.</p> : null}
      {openCode.data?.state === "unavailable" ? <p className="mb-3 text-small">OpenCode is unavailable. <Link className="underline" to="/settings">Check connection</Link></p> : null}
      {openCode.data?.state === "rejected" ? <p className="mb-3 text-small">OpenCode no longer accepts Stage's sign-in. <Link className="underline" to="/settings">Reconnect</Link></p> : null}
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
  const meta = [tasks.data === undefined ? null : owed === 0 ? "nothing needs you" : `${owed} need${owed === 1 ? "s" : ""} you`, where]
    .filter((s) => s !== null)
    .join(" · ");

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
      <QueryState isPending={tasks.isPending} error={tasks.error}>
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
                  group={key}
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
        <InFlight
          cells={[
            { key: "running", label: "Running", tone: "run", count: of("running").length },
            { key: "scheduled", label: "Scheduled · 24h", tone: "idle", count: of("scheduled").length + (dueSchedules.data?.schedules.length ?? 0) },
            { key: "backlog", label: "Backlog", tone: "idle", count: of("backlog").length },
          ]}
        />
      </QueryState>
    </>
  );
}
