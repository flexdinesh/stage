import { useSearchParams } from "react-router";
import { useProjects, useT3Status, useTasks } from "../api/queries.ts";
import type { Task, TaskState } from "../api/types.ts";
import { IconExternal } from "../components/icons.tsx";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { TaskRows } from "../components/TaskList.tsx";
import { ExternalButton, PageHeader, Panel, QueryState, cx, toneClass, type ChipTone } from "../components/ui.tsx";

type GroupKey = "needs" | "running" | "completed" | "failed" | "backlog";

interface Group {
  key: GroupKey;
  title: string;
  stat: string;
  short: string;
  tone: ChipTone;
  states: TaskState[];
  empty: string;
  hideEmpty?: boolean;
}

const GROUPS: Group[] = [
  {
    key: "needs",
    title: "Needs input",
    stat: "Needs input",
    short: "need",
    tone: "need",
    states: ["needs_input"],
    empty: "Nothing waiting on you. Tasks that ask a question land here.",
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
    key: "completed",
    title: "Completed · awaiting settle in T3",
    stat: "Awaiting settle",
    short: "settle",
    tone: "done",
    states: ["completed"],
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
];

const ALL_STATES = GROUPS.flatMap((g) => g.states);
const ACTIVE_STATES: readonly TaskState[] = ["launching", "running", "needs_input"];

const parseGroup = (v: string | null): GroupKey | null => GROUPS.find((g) => g.key === v)?.key ?? null;

function NeedsOpenLink({ tasks }: { tasks: Task[] }) {
  const t3 = useT3Status();
  const first = tasks[0];
  if (first === undefined) return null;
  const href = tasks.length === 1 ? first.t3WebUrl : (t3.data?.publicUrl ?? null);
  if (href === null) return null;
  return (
    <ExternalButton href={href} small variant="ghost">
      Open in T3
      <IconExternal size={12} />
    </ExternalButton>
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
          <span className="min-w-0 text-[12.5px] text-muted">{g.empty}</span>
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
              <span className={cx("tabular text-[15px] font-bold", on ? "text-[var(--cf)]" : n === 0 ? "text-muted" : "text-fg")}>
                {n}
              </span>
            </span>
            <span className="label-caps text-[10.5px] md:hidden">{g.short}</span>
            <span className="label-caps hidden md:inline">{g.stat}</span>
          </button>
        );
      })}
      <div className="hidden flex-1 items-center justify-end px-3 md:flex">
        {selected === null ? (
          <span className="font-mono text-[11px] text-muted">click a count to filter</span>
        ) : (
          <button
            type="button"
            onClick={() => onToggle(selected)}
            className="font-mono text-[11px] text-muted underline-offset-3 hover:text-fg hover:underline"
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
  const tasks = useTasks({ state: ALL_STATES, ...(project === "" ? {} : { project: Number(project) }) });
  const all = tasks.data?.tasks ?? [];
  const byGroup = (g: Group) => all.filter((t) => g.states.includes(t.state));

  const counts: Record<GroupKey, number> = { needs: 0, running: 0, completed: 0, failed: 0, backlog: 0 };
  for (const g of GROUPS) counts[g.key] = byGroup(g).length;
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
    const empty = byGroup(g).length === 0;
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
      <QueryState isPending={tasks.isPending} error={tasks.error}>
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
            return (
              <Panel
                key={g.key}
                flush
                title={
                  <span className={cx(toneClass(g.tone), "inline-flex items-center gap-2")}>
                    <span aria-hidden className="size-1.5 bg-[var(--c)]" />
                    {g.title}
                  </span>
                }
                meta={<span className="tabular">{list.length}</span>}
                {...(g.key === "needs" && list.length > 0 ? { actions: <NeedsOpenLink tasks={list} /> } : {})}
              >
                <TaskRows tasks={list} emptyText={g.empty} />
              </Panel>
            );
          })}
        </div>
      </QueryState>
    </>
  );
}
