import { useState } from "react";
import { useSearchParams } from "react-router";
import { useNeedsInputThreads, useProjects, useRunningThreads, useTasks } from "../api/queries.ts";
import type { Task, TaskState, Thread } from "../api/types.ts";
import { PendingContinuationsPanel } from "../components/Continuations.tsx";
import { IconChevronDown } from "../components/icons.tsx";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { ExternalThreadRows, TaskRows, type ExternalThreadState } from "../components/TaskList.tsx";
import { AppButton, ErrorText, PageHeader, Panel, QueryState, cx, toneClass, type ChipTone } from "../components/ui.tsx";

type GroupKey = "needs" | "running" | "done" | "failed" | "backlog" | "settled";

interface Group {
  key: GroupKey;
  title: string;
  stat: string;
  short: string;
  tone: ChipTone;
  states: TaskState[];
  empty: string;
  hideEmpty?: boolean;
  /** Rendered collapsed until opened (or filtered to). */
  collapsible?: boolean;
}

const GROUPS: Group[] = [
  {
    key: "needs",
    title: "Needs input",
    stat: "Needs input",
    short: "need",
    tone: "need",
    states: ["needs_input"],
    empty: "Nothing waiting on you. Tasks and threads that ask a question land here.",
  },
  {
    key: "running",
    title: "Running",
    stat: "Running",
    short: "run",
    tone: "run",
    states: ["launching", "running"],
    empty: "Nothing running. Start a task from the backlog.",
  },
  {
    key: "done",
    title: "Done · awaiting settle in T3",
    stat: "Awaiting settle",
    short: "settle",
    tone: "done",
    states: ["done"],
    empty: "Nothing to settle. Finished tasks wait here until settled in T3.",
  },
  {
    key: "failed",
    title: "Failed / interrupted",
    stat: "Failed",
    short: "fail",
    tone: "bad",
    states: ["failed", "interrupted"],
    empty: "No failed or interrupted tasks.",
    hideEmpty: true,
  },
  {
    key: "backlog",
    title: "Backlog",
    stat: "Backlog",
    short: "backlog",
    tone: "idle",
    states: ["backlog"],
    empty: "Backlog is empty. Add one with New task.",
  },
  {
    key: "settled",
    title: "Settled · last 7 days",
    stat: "Settled",
    short: "settled",
    tone: "ok",
    states: ["settled"],
    empty: "Nothing settled in the last 7 days.",
    collapsible: true,
  },
];

// Settled tasks come from their own windowed query (SETTLED_DAYS); the rest are current totals.
const LIVE_STATES = GROUPS.flatMap((g) => g.states).filter((s) => s !== "settled");
const SETTLED_DAYS = 7;

/** Start of the settled window, floored to the hour so the query key stays stable between renders. */
function settledSince(): string {
  const d = new Date(Date.now() - SETTLED_DAYS * 24 * 3600 * 1000);
  d.setMinutes(0, 0, 0);
  return d.toISOString();
}
const ACTIVE_STATES: readonly TaskState[] = ["launching", "running", "needs_input"];

/** External (non-task) threads a group lists after its tasks. */
interface ExternalGroup {
  threads: Thread[];
  error: unknown;
  state: ExternalThreadState;
}

const parseGroup = (v: string | null): GroupKey | null => GROUPS.find((g) => g.key === v)?.key ?? null;

function NeedsOpenLink({ tasks }: { tasks: Task[] }) {
  // App links open one thread, so only a lone task gets one.
  const href = tasks.length === 1 ? (tasks[0]?.t3DeepLink ?? null) : null;
  if (href === null) return null;
  return (
    <AppButton href={href} small variant="ghost">
      Open in app
    </AppButton>
  );
}

function EmptyGroups({ groups }: { groups: Group[] }) {
  return (
    <ul className="border border-line bg-surface md:grid md:grid-cols-[max-content_minmax(0,1fr)]">
      {groups.map((g) => (
        <li
          key={g.key}
          className="flex min-w-0 flex-col gap-0.5 border-b border-line px-3 py-2 last:border-b-0 md:col-span-2 md:grid md:grid-cols-subgrid md:items-baseline md:gap-x-6"
        >
          <span className="label-caps inline-flex items-center gap-2 text-fg">
            <span aria-hidden className="size-1.5 bg-line-strong" />
            {g.title}
            <span className="tabular text-muted">0</span>
          </span>
          <span className="min-w-0 text-small text-muted">{g.empty}</span>
        </li>
      ))}
    </ul>
  );
}

function StatStrip({
  counts,
  selected,
  onToggle,
}: {
  counts: Record<GroupKey, number>;
  selected: GroupKey | null;
  onToggle: (key: GroupKey) => void;
}) {
  return (
    <div role="group" aria-label="Filter by state" className="mb-4 flex border border-line bg-surface">
      {GROUPS.map((g) => {
        const on = selected === g.key;
        const n = counts[g.key];
        return (
          <button
            key={g.key}
            type="button"
            aria-pressed={on}
            title={g.stat}
            onClick={() => onToggle(g.key)}
            className={cx(
              toneClass(g.tone),
              "flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 border-r border-line px-1 py-1.5 whitespace-nowrap max-md:last-of-type:border-r-0 transition-colors duration-150 md:flex-none md:flex-row md:justify-start md:gap-2 md:px-3",
              on ? "bg-[color-mix(in_srgb,var(--c)_10%,transparent)] shadow-[inset_0_-2px_var(--c)]" : "hover:bg-panel",
            )}
          >
            <span className="flex items-center gap-1.5">
              {n > 0 ? <span aria-hidden className="size-1.5 bg-[var(--c)]" /> : null}
              <span className={cx("tabular text-title font-bold", on ? "text-[var(--cf)]" : n === 0 ? "text-muted" : "text-fg")}>
                {n}
              </span>
            </span>
            <span className="label-caps text-label md:hidden">{g.short}</span>
            <span className="label-caps hidden md:inline">{g.stat}</span>
          </button>
        );
      })}
      <div className="hidden flex-1 items-center justify-end px-3 md:flex">
        {selected === null ? (
          <span className="font-mono text-meta-sm text-muted">click a count to filter</span>
        ) : (
          <button
            type="button"
            onClick={() => onToggle(selected)}
            className="font-mono text-meta-sm text-muted underline-offset-3 hover:text-fg hover:underline"
          >
            clear filter
          </button>
        )}
      </div>
    </div>
  );
}

export function DashboardPage() {
  const [params, setParams] = useSearchParams();
  const project = params.get("project") ?? "";
  const selected = parseGroup(params.get("state"));
  const projects = useProjects();
  const projectQuery = project === "" ? {} : { project: Number(project) };
  const tasks = useTasks({ state: LIVE_STATES, ...projectQuery });
  // External threads needing input / running join the Needs input / Running groups (count and rows)
  // but never block the page.
  const projectId = project === "" ? undefined : Number(project);
  const needThreads = useNeedsInputThreads(projectId);
  const runThreads = useRunningThreads(projectId);
  const external: Partial<Record<GroupKey, ExternalGroup>> = {
    needs: { threads: needThreads.data?.threads ?? [], error: needThreads.error, state: "needs_input" },
    running: { threads: runThreads.data?.threads ?? [], error: runThreads.error, state: "running" },
  };
  const [since] = useState(settledSince);
  const settled = useTasks({ state: ["settled"], settledFrom: since, ...projectQuery });
  const [settledOpen, setSettledOpen] = useState(false);
  const all = [...(tasks.data?.tasks ?? []), ...(settled.data?.tasks ?? [])];
  const byGroup = (g: Group) => all.filter((t) => g.states.includes(t.state));
  const groupCount = (g: Group) => byGroup(g).length + (external[g.key]?.threads.length ?? 0);

  const counts: Record<GroupKey, number> = { needs: 0, running: 0, done: 0, failed: 0, backlog: 0, settled: 0 };
  for (const g of GROUPS) counts[g.key] = groupCount(g);
  const active = all.filter((t) => ACTIVE_STATES.includes(t.state)).length;
  const projectCount = projects.data?.projects.length;

  function setParam(key: string, value: string | null) {
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === null || value === "") next.delete(key);
      else next.set(key, value);
      return next;
    });
  }

  // Groups in order; consecutive empty groups (unfiltered view) collapse into one ruled list.
  const blocks: (Group | Group[])[] = [];
  for (const g of GROUPS) {
    if (selected !== null && selected !== g.key) continue;
    const empty = groupCount(g) === 0;
    if (selected === null && empty && g.hideEmpty === true) continue;
    const prev = blocks.at(-1);
    if (selected === null && empty) {
      if (Array.isArray(prev)) prev.push(g);
      else blocks.push([g]);
    } else blocks.push(g);
  }

  const meta = [
    projectCount === undefined ? null : `${projectCount} project${projectCount === 1 ? "" : "s"}`,
    tasks.data === undefined ? null : `${active} active`,
  ]
    .filter((s) => s !== null)
    .join(" · ");

  return (
    <>
      <PageHeader
        title="Dashboard"
        {...(meta === "" ? {} : { meta })}
        actions={<ProjectSelect value={project} onChange={(v) => setParam("project", v)} />}
      />
      <PendingContinuationsPanel project={projectId} />
      <QueryState isPending={tasks.isPending || settled.isPending} error={tasks.error ?? settled.error}>
        <StatStrip
          counts={counts}
          selected={selected}
          onToggle={(key) => setParam("state", selected === key ? null : key)}
        />
        <div className="flex flex-col gap-3.5">
          {blocks.map((block) => {
            if (Array.isArray(block)) return <EmptyGroups key={block.map((g) => g.key).join("-")} groups={block} />;
            const g = block;
            const list = byGroup(g);
            const ext = external[g.key];
            const collapsed = g.collapsible === true && selected !== g.key && !settledOpen;
            // self-baseline: flex baseline comes from the text, not the square/chevron (no baseline → bottom edge),
            // so Panel's cap-trimmed h2 centers the caps.
            const label = (
              <span className={cx(toneClass(g.tone), "inline-flex items-center gap-2 self-baseline")}>
                <span aria-hidden className="size-1.5 bg-[var(--c)]" />
                <span className="self-baseline">{g.title}</span>
              </span>
            );
            return (
              <Panel
                key={g.key}
                flush
                collapsed={collapsed}
                title={
                  g.collapsible === true && selected !== g.key ? (
                    <button
                      type="button"
                      aria-expanded={!collapsed}
                      onClick={() => setSettledOpen((v) => !v)}
                      className="label-caps inline-flex items-center gap-1.5 text-fg"
                    >
                      <span aria-hidden className={cx("inline-flex transition-transform duration-150", collapsed && "-rotate-90")}>
                        <IconChevronDown size={12} />
                      </span>
                      {label}
                    </button>
                  ) : (
                    label
                  )
                }
                meta={<span className="tabular">{groupCount(g)}</span>}
                {...(g.key === "needs" && list.length > 0 ? { actions: <NeedsOpenLink tasks={list} /> } : {})}
              >
                {ext !== undefined && ext.threads.length > 0 ? (
                  <>
                    {list.length > 0 ? (
                      <div className="border-b border-line">
                        <TaskRows tasks={list} showProject />
                      </div>
                    ) : null}
                    <ExternalThreadRows threads={ext.threads} state={ext.state} />
                  </>
                ) : (
                  <TaskRows tasks={list} emptyText={g.empty} showProject />
                )}
                {ext !== undefined && ext.error !== null ? (
                  <div className="border-t border-line p-3">
                    <ErrorText error={ext.error} />
                  </div>
                ) : null}
              </Panel>
            );
          })}
        </div>
      </QueryState>
    </>
  );
}
